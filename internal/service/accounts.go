package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/store"
)

// ErrBootstrapCredentials is returned when the database is empty and the
// first administrator cannot be created.
var ErrBootstrapCredentials = errors.New("database has no users: set ADMIN_EMAIL and ADMIN_PASSWORD to create the first administrator")

// Bootstrap creates the first administrator when the database has no users.
// It reports whether an administrator was created.
func (s *Service) Bootstrap(ctx context.Context) (bool, error) {
	n, err := s.repo.CountUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	if n > 0 {
		return false, nil
	}
	if s.cfg.AdminEmail == "" || s.cfg.AdminPassword == "" {
		missing := []string{}
		if s.cfg.AdminEmail == "" {
			missing = append(missing, "ADMIN_EMAIL")
		}
		if s.cfg.AdminPassword == "" {
			missing = append(missing, "ADMIN_PASSWORD")
		}
		return false, fmt.Errorf("%w (missing: %s)", ErrBootstrapCredentials, strings.Join(missing, ", "))
	}
	hash, err := s.hasher.hash(ctx, s.cfg.AdminPassword)
	if err != nil {
		return false, err
	}
	now := s.now()
	id, err := s.repo.CreateUser(ctx, domain.User{
		Email: s.cfg.AdminEmail, PasswordHash: hash, Role: domain.RoleAdmin,
		Status: domain.UserActive, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return false, fmt.Errorf("create first administrator: %w", err)
	}
	s.audit(ctx, Actor{User: domain.User{ID: id}}, "user.bootstrap", userTarget(id), map[string]any{"email": s.cfg.AdminEmail})
	return true, nil
}

func userTarget(id int64) string { return fmt.Sprintf("user:%d", id) }

// InviteLink is a freshly issued invite; Secret is shown only once.
type InviteLink struct {
	User      domain.User
	URL       string
	ExpiresAt string    // human-readable, minute precision
	Expires   time.Time // exact expiry
}

// CreateUser creates an invited user and issues an invite link (admin only).
func (s *Service) CreateUser(ctx context.Context, a Actor, email string, role domain.Role) (InviteLink, error) {
	if err := requireAdmin(a); err != nil {
		return InviteLink{}, err
	}
	email, err := domain.NormalizeEmail(email)
	if err != nil {
		return InviteLink{}, err
	}
	if role == "" {
		role = domain.RoleUser
	}
	if role != domain.RoleUser && role != domain.RoleAdmin {
		return InviteLink{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput, "role must be user or admin")
	}
	now := s.now()
	id, err := s.repo.CreateUser(ctx, domain.User{
		Email: email, Role: role, Status: domain.UserInvited, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return InviteLink{}, err
	}
	s.audit(ctx, a, "user.create", userTarget(id), map[string]any{"email": email, "role": role})
	return s.issueInvite(ctx, a, id)
}

// ReissueInvite revokes previous invites and issues a new link (admin only).
// For an active user it works as a password reset.
func (s *Service) ReissueInvite(ctx context.Context, a Actor, userID int64) (InviteLink, error) {
	if err := requireAdmin(a); err != nil {
		return InviteLink{}, err
	}
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return InviteLink{}, err
	}
	if u.Status == domain.UserBlocked {
		return InviteLink{}, domain.E(domain.KindInvalid, domain.CodeUserBlocked, "unblock the user before issuing an invite")
	}
	return s.issueInvite(ctx, a, userID)
}

// RevokeUserTokens revokes every active API token of a user (admin only), for
// example after a password reset when the account may have been compromised.
// Reissuing an invite deliberately does not do this on its own, so CI tokens
// keep working through an ordinary password reset. It returns the number of
// tokens revoked.
func (s *Service) RevokeUserTokens(ctx context.Context, a Actor, userID int64) (int, error) {
	if err := requireAdmin(a); err != nil {
		return 0, err
	}
	if _, err := s.repo.GetUser(ctx, userID); err != nil {
		return 0, err
	}
	n, err := s.repo.RevokeUserTokens(ctx, userID, s.now())
	if err != nil {
		return 0, err
	}
	s.audit(ctx, a, "user.tokens_revoke", userTarget(userID), map[string]any{"count": n})
	return n, nil
}

func (s *Service) issueInvite(ctx context.Context, a Actor, userID int64) (InviteLink, error) {
	secret := domain.NewSecret()
	now := s.now()
	inv := domain.Invite{
		UserID: userID, Hash: domain.HashSecret(secret), CreatedBy: a.User.ID,
		CreatedAt: now, ExpiresAt: now.Add(InviteTTL),
	}
	if _, err := s.repo.CreateInvite(ctx, inv); err != nil {
		return InviteLink{}, fmt.Errorf("create invite: %w", err)
	}
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return InviteLink{}, err
	}
	s.audit(ctx, a, "user.invite", userTarget(userID), map[string]any{"email": u.Email})
	return InviteLink{User: u, URL: s.CabinetURL(ctx) + "/invite/" + secret, ExpiresAt: inv.ExpiresAt.Format("2006-01-02 15:04 MST"), Expires: inv.ExpiresAt}, nil
}

var errInviteInvalid = domain.E(domain.KindInvalid, domain.CodeInviteInvalid,
	"this invite link is invalid or has expired; ask an administrator for a new one")

// InviteUser returns the user an invite secret belongs to, if usable.
func (s *Service) InviteUser(ctx context.Context, secret string) (domain.User, error) {
	inv, err := s.repo.GetInviteByHash(ctx, domain.HashSecret(secret))
	if err != nil || !inv.Usable(s.now()) {
		return domain.User{}, errInviteInvalid
	}
	u, err := s.repo.GetUser(ctx, inv.UserID)
	if err != nil || u.Status == domain.UserBlocked {
		return domain.User{}, errInviteInvalid
	}
	return u, nil
}

// RedeemInvite sets the password from an invite and activates the user.
// Existing sessions of the user are terminated.
func (s *Service) RedeemInvite(ctx context.Context, secret, password, ip string) (domain.User, error) {
	if err := domain.ValidatePassword(password); err != nil {
		return domain.User{}, err
	}
	inv, err := s.repo.GetInviteByHash(ctx, domain.HashSecret(secret))
	if err != nil || !inv.Usable(s.now()) {
		return domain.User{}, errInviteInvalid
	}
	hash, err := s.hasher.hash(ctx, password)
	if err != nil {
		return domain.User{}, err
	}
	u, err := s.repo.RedeemInvite(ctx, inv.ID, hash, s.now())
	if err != nil {
		if de := domain.AsError(err); de.Kind != domain.KindInternal {
			return domain.User{}, errInviteInvalid
		}
		return domain.User{}, err
	}
	if err := s.repo.DeleteUserSessions(ctx, u.ID); err != nil {
		return domain.User{}, fmt.Errorf("end sessions: %w", err)
	}
	s.audit(ctx, Actor{User: u, IP: ip}, "user.activate", userTarget(u.ID), nil)
	return u, nil
}

// ResetPassword sets a new password for the user with the given email. It is
// an operator action run from the server shell (control reset-password) and
// is not reachable through the API or the web interface: whoever can run it
// already has the database. An invited user becomes active; a blocked user
// stays blocked. Every session of the user ends.
func (s *Service) ResetPassword(ctx context.Context, email, password string) (domain.User, error) {
	if err := domain.ValidatePassword(password); err != nil {
		return domain.User{}, err
	}
	u, err := s.repo.GetUserByEmail(ctx, strings.TrimSpace(email))
	if err != nil {
		return domain.User{}, err
	}
	hash, err := s.hasher.hash(ctx, password)
	if err != nil {
		return domain.User{}, err
	}
	now := s.now()
	if err := s.repo.SetUserPassword(ctx, u.ID, hash, now); err != nil {
		return domain.User{}, err
	}
	if u.Status == domain.UserInvited {
		if err := s.repo.SetUserStatus(ctx, u.ID, domain.UserActive, now); err != nil {
			return domain.User{}, err
		}
	}
	if err := s.repo.DeleteUserSessions(ctx, u.ID); err != nil {
		return domain.User{}, fmt.Errorf("end sessions: %w", err)
	}
	s.audit(ctx, Actor{}, "user.password_reset", userTarget(u.ID),
		map[string]any{"email": u.Email, "source": string(domain.SourceCLI)})
	return s.repo.GetUser(ctx, u.ID)
}

// SetUserBlocked blocks or unblocks a user (admin only). Blocking ends all
// sessions at once; tokens stop working because authentication checks status.
func (s *Service) SetUserBlocked(ctx context.Context, a Actor, userID int64, blocked bool) (domain.User, error) {
	if err := requireAdmin(a); err != nil {
		return domain.User{}, err
	}
	if userID == a.User.ID {
		return domain.User{}, domain.E(domain.KindInvalid, domain.CodeSelfAction, "you cannot block or unblock yourself")
	}
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return domain.User{}, err
	}
	status := domain.UserBlocked
	action := "user.block"
	if !blocked {
		action = "user.unblock"
		status = domain.UserActive
		if u.PasswordHash == "" {
			status = domain.UserInvited
		}
	}
	if err := s.repo.SetUserStatus(ctx, userID, status, s.now()); err != nil {
		return domain.User{}, err
	}
	if blocked {
		if err := s.repo.DeleteUserSessions(ctx, userID); err != nil {
			return domain.User{}, fmt.Errorf("end sessions: %w", err)
		}
	}
	s.audit(ctx, a, action, userTarget(userID), map[string]any{"email": u.Email})
	return s.repo.GetUser(ctx, userID)
}

// ListUsers lists users (admin only).
func (s *Service) ListUsers(ctx context.Context, a Actor, f store.UserFilter) ([]store.UserSummary, int, error) {
	if err := requireAdmin(a); err != nil {
		return nil, 0, err
	}
	return s.repo.ListUsers(ctx, f)
}

// GetUser returns a user (admin only).
func (s *Service) GetUser(ctx context.Context, a Actor, userID int64) (domain.User, error) {
	if err := requireAdmin(a); err != nil {
		return domain.User{}, err
	}
	return s.repo.GetUser(ctx, userID)
}

// SetLang stores the actor's UI language.
func (s *Service) SetLang(ctx context.Context, a Actor, lang string) error {
	return s.repo.SetUserLang(ctx, a.User.ID, lang, s.now())
}
