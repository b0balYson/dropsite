package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// FormRepository stores form channels, submissions and the delivery queue.
type FormRepository interface {
	// GetSiteByHost resolves an active (verified) hostname to its site.
	GetSiteByHost(ctx context.Context, host string) (domain.Site, error)
	GetFormSiteSettings(ctx context.Context, siteID string) (domain.FormSiteSettings, error)
	SetFormSiteSettings(ctx context.Context, s domain.FormSiteSettings) error

	CountFormChannels(ctx context.Context, siteID string) (int, error)
	CreateFormChannel(ctx context.Context, ch domain.FormChannel) (int64, error)
	// GetFormChannel returns the channel only if it belongs to siteID.
	GetFormChannel(ctx context.Context, siteID string, id int64) (domain.FormChannel, error)
	ListFormChannels(ctx context.Context, siteID string) ([]domain.FormChannel, error)
	// UpdateFormChannel stores every mutable column of ch (matched by ID and SiteID).
	UpdateFormChannel(ctx context.Context, ch domain.FormChannel) error
	DeleteFormChannel(ctx context.Context, siteID string, id int64) error
	// ListPendingTelegramLinks returns link-based Telegram bindings that
	// have not expired at now.
	ListPendingTelegramLinks(ctx context.Context, now time.Time) ([]domain.FormChannel, error)

	// InsertFormSubmission stores sub and queues one delivery per active,
	// enabled channel of its site, atomically. It returns the new ID.
	InsertFormSubmission(ctx context.Context, sub domain.FormSubmission) (int64, error)
	ListFormSubmissions(ctx context.Context, f FormSubmissionFilter) ([]domain.FormSubmission, int, error)
	ListFormNames(ctx context.Context, siteID string) ([]string, error)

	// ConsumeVerifyAttempt atomically counts one confirmation attempt of a
	// pending channel and returns the channel as it was stored before the
	// attempt. ok is false when the channel is not pending or has used max
	// attempts already.
	ConsumeVerifyAttempt(ctx context.Context, siteID string, id int64, max int, now time.Time) (ch domain.FormChannel, ok bool, err error)
	// ActivateFormChannel confirms a pending channel; false if it was not pending.
	ActivateFormChannel(ctx context.Context, siteID string, id int64, now time.Time) (bool, error)
	// ResumeFormChannel clears the circuit breaker of a channel.
	ResumeFormChannel(ctx context.Context, siteID string, id int64, now time.Time) error
	// RecordChannelResult resets (ok) or increments the channel's run of
	// failures; when a failure brings it to pauseAfter (> 0) the channel is
	// paused at now. It reports whether this call paused the channel.
	RecordChannelResult(ctx context.Context, channelID int64, ok bool, pauseAfter int, now time.Time) (bool, error)

	// ClaimDueDeliveries cancels queued deliveries of turned-off channels and
	// deleted sites, then leases due deliveries fairly (see DeliveryClaim).
	ClaimDueDeliveries(ctx context.Context, c DeliveryClaim) ([]DeliveryJob, error)
	FinishDelivery(ctx context.Context, d domain.FormDelivery) error
	// ListSubmissionsBefore returns up to limit submissions of the site (and
	// form, if set) with ID below beforeID, newest first, without deliveries.
	ListSubmissionsBefore(ctx context.Context, siteID, form string, beforeID int64, limit int) ([]domain.FormSubmission, error)

	// UseAltchaSolution records a solution signature; a second use before
	// it expires returns false.
	UseAltchaSolution(ctx context.Context, signature string, expires time.Time) (bool, error)
	// PurgeForms deletes submissions created before cutoff, everything of
	// deleted sites and expired Altcha records.
	PurgeForms(ctx context.Context, cutoff, now time.Time) (int, error)
}

// FormSubmissionFilter filters ListFormSubmissions; newest first.
type FormSubmissionFilter struct {
	SiteID string
	Form   string
	Page
}

// DeliveryClaim selects deliveries to lease. At most one due delivery per
// channel is considered, channels in SkipChannels (attempts in flight) and
// paused channels are left out, and candidates are ordered so that every
// site gets its oldest delivery before any site gets a second one. Accept,
// if set, may still refuse a candidate (per-site caps); only accepted ones
// are leased, up to Limit.
type DeliveryClaim struct {
	Now, LeaseUntil time.Time
	Limit           int
	SkipChannels    []int64
	Accept          func(DeliveryJob) bool
}

// DeliveryCancelledMsg is the last_error of cancelled deliveries.
const DeliveryCancelledMsg = "not sent: the channel was turned off or the site was deleted"

// DeliveryJob is a claimed delivery with what the sender needs.
type DeliveryJob struct {
	Delivery   domain.FormDelivery
	Channel    domain.FormChannel
	Submission domain.FormSubmission
	OwnerLang  string
}

const channelCols = `id, site_id, kind, target, secret_enc, status, enabled, verify_hash,
	verify_expires_at, verify_attempts, consecutive_failures, paused_at, created_at, updated_at`

func scanChannel(s rowScanner) (domain.FormChannel, error) {
	var c domain.FormChannel
	var expires, paused sql.NullInt64
	var created, updated int64
	if err := s.Scan(&c.ID, &c.SiteID, &c.Kind, &c.Target, &c.SecretEnc, &c.Status, &c.Enabled,
		&c.VerifyHash, &expires, &c.VerifyAttempts, &c.ConsecutiveFailures, &paused, &created, &updated); err != nil {
		return domain.FormChannel{}, err
	}
	c.VerifyExpiresAt, c.PausedAt = fromNullMS(expires), fromNullMS(paused)
	c.CreatedAt, c.UpdatedAt = fromMS(created), fromMS(updated)
	return c, nil
}

// GetSiteByHost resolves an active hostname to its site.
func (db *DB) GetSiteByHost(ctx context.Context, host string) (domain.Site, error) {
	row := db.r.QueryRowContext(ctx, `SELECT `+siteColsS+` FROM hostnames h
		JOIN sites s ON s.id = h.site_id
		WHERE h.hostname = ? AND h.verified = 1 AND s.deleted_at IS NULL`, host)
	site, err := scanSite(row)
	if err != nil {
		return domain.Site{}, mapNoRows(err, "get site by host")
	}
	return site, nil
}

// GetFormSiteSettings returns the site's form options (defaults if unset).
func (db *DB) GetFormSiteSettings(ctx context.Context, siteID string) (domain.FormSiteSettings, error) {
	s := domain.FormSiteSettings{SiteID: siteID}
	err := db.r.QueryRowContext(ctx, "SELECT require_altcha FROM form_site_settings WHERE site_id = ?", siteID).
		Scan(&s.RequireAltcha)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return s, fmt.Errorf("get form settings: %w", err)
	}
	return s, nil
}

// SetFormSiteSettings upserts the site's form options.
func (db *DB) SetFormSiteSettings(ctx context.Context, s domain.FormSiteSettings) error {
	_, err := db.w.ExecContext(ctx, `INSERT INTO form_site_settings (site_id, require_altcha) VALUES (?, ?)
		ON CONFLICT (site_id) DO UPDATE SET require_altcha = excluded.require_altcha`, s.SiteID, s.RequireAltcha)
	if err != nil {
		return fmt.Errorf("set form settings: %w", err)
	}
	return nil
}

// CountFormChannels counts the site's channels.
func (db *DB) CountFormChannels(ctx context.Context, siteID string) (int, error) {
	var n int
	if err := db.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM form_channels WHERE site_id = ?", siteID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count form channels: %w", err)
	}
	return n, nil
}

// CreateFormChannel inserts ch and returns its ID.
func (db *DB) CreateFormChannel(ctx context.Context, ch domain.FormChannel) (int64, error) {
	res, err := db.w.ExecContext(ctx, `INSERT INTO form_channels (site_id, kind, target, secret_enc, status, enabled,
		verify_hash, verify_expires_at, verify_attempts, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ch.SiteID, string(ch.Kind), ch.Target, ch.SecretEnc, string(ch.Status), ch.Enabled, ch.VerifyHash,
		nullMS(ch.VerifyExpiresAt), ch.VerifyAttempts, toMS(ch.CreatedAt), toMS(ch.UpdatedAt))
	if err != nil {
		return 0, fmt.Errorf("create form channel: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("create form channel: %w", err)
	}
	return id, nil
}

// GetFormChannel returns a channel of siteID.
func (db *DB) GetFormChannel(ctx context.Context, siteID string, id int64) (domain.FormChannel, error) {
	c, err := scanChannel(db.r.QueryRowContext(ctx, "SELECT "+channelCols+" FROM form_channels WHERE id = ? AND site_id = ?", id, siteID))
	if err != nil {
		return domain.FormChannel{}, mapNoRows(err, "get form channel")
	}
	return c, nil
}

func (db *DB) queryChannels(ctx context.Context, where string, args ...any) ([]domain.FormChannel, error) {
	query := "SELECT " + channelCols + " FROM form_channels WHERE " + where + " ORDER BY id" //nolint:gosec // G202: where is a constant chosen by callers in this file
	rows, err := db.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list form channels: %w", err)
	}
	defer rows.Close()
	var out []domain.FormChannel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("scan form channel: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list form channels: %w", err)
	}
	return out, nil
}

// ListFormChannels returns the site's channels in creation order.
func (db *DB) ListFormChannels(ctx context.Context, siteID string) ([]domain.FormChannel, error) {
	return db.queryChannels(ctx, "site_id = ?", siteID)
}

// ListPendingTelegramLinks returns unexpired link-based Telegram bindings.
func (db *DB) ListPendingTelegramLinks(ctx context.Context, now time.Time) ([]domain.FormChannel, error) {
	return db.queryChannels(ctx, `kind = 'telegram' AND status = 'pending' AND target = ''
		AND verify_expires_at > ? AND site_id IN (SELECT id FROM sites WHERE deleted_at IS NULL)`, toMS(now))
}

// UpdateFormChannel stores the mutable columns of ch. The attempt counter
// and the circuit breaker columns are changed only atomically, by
// ConsumeVerifyAttempt, RecordChannelResult and ResumeFormChannel.
func (db *DB) UpdateFormChannel(ctx context.Context, ch domain.FormChannel) error {
	res, err := db.w.ExecContext(ctx, `UPDATE form_channels SET target = ?, secret_enc = ?, status = ?, enabled = ?,
		verify_hash = ?, verify_expires_at = ?, updated_at = ? WHERE id = ? AND site_id = ?`,
		ch.Target, ch.SecretEnc, string(ch.Status), ch.Enabled, ch.VerifyHash, nullMS(ch.VerifyExpiresAt),
		toMS(ch.UpdatedAt), ch.ID, ch.SiteID)
	return expectAffected(res, err, "update form channel")
}

// DeleteFormChannel removes a channel and its deliveries.
func (db *DB) DeleteFormChannel(ctx context.Context, siteID string, id int64) error {
	res, err := db.w.ExecContext(ctx, "DELETE FROM form_channels WHERE id = ? AND site_id = ?", id, siteID)
	return expectAffected(res, err, "delete form channel")
}

// InsertFormSubmission stores sub and queues its deliveries.
func (db *DB) InsertFormSubmission(ctx context.Context, sub domain.FormSubmission) (int64, error) {
	fields, err := json.Marshal(sub.Fields)
	if err != nil {
		return 0, fmt.Errorf("encode form fields: %w", err)
	}
	var id int64
	err = db.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO form_submissions (site_id, host, form, fields_json, created_at)
			VALUES (?, ?, ?, ?, ?)`, sub.SiteID, sub.Host, sub.Form, string(fields), toMS(sub.CreatedAt))
		if err != nil {
			return fmt.Errorf("insert form submission: %w", err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("insert form submission: %w", err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO form_deliveries (submission_id, channel_id, status, next_attempt_at)
			SELECT ?, id, 'pending', ? FROM form_channels WHERE site_id = ? AND status = 'active' AND enabled = 1`,
			id, toMS(sub.CreatedAt), sub.SiteID)
		if err != nil {
			return fmt.Errorf("queue form deliveries: %w", err)
		}
		return nil
	})
	return id, err
}

const deliveryCols = `d.id, d.submission_id, d.channel_id, c.kind, c.target, d.status, d.attempts,
	d.next_attempt_at, d.last_error, d.sent_at`

func scanDelivery(s rowScanner) (domain.FormDelivery, error) {
	var d domain.FormDelivery
	var next int64
	var sent sql.NullInt64
	if err := s.Scan(&d.ID, &d.SubmissionID, &d.ChannelID, &d.ChannelKind, &d.ChannelTarget, &d.Status,
		&d.Attempts, &next, &d.LastError, &sent); err != nil {
		return domain.FormDelivery{}, err
	}
	d.NextAttemptAt, d.SentAt = fromMS(next), fromNullMS(sent)
	return d, nil
}

func scanSubmission(s rowScanner) (domain.FormSubmission, error) {
	var sub domain.FormSubmission
	var fields string
	var created int64
	if err := s.Scan(&sub.ID, &sub.SiteID, &sub.Host, &sub.Form, &fields, &created); err != nil {
		return domain.FormSubmission{}, err
	}
	if err := json.Unmarshal([]byte(fields), &sub.Fields); err != nil {
		return domain.FormSubmission{}, fmt.Errorf("decode form fields: %w", err)
	}
	sub.CreatedAt = fromMS(created)
	return sub, nil
}

const submissionCols = "id, site_id, host, form, fields_json, created_at"

// ListFormSubmissions returns a page of submissions with their deliveries.
func (db *DB) ListFormSubmissions(ctx context.Context, f FormSubmissionFilter) ([]domain.FormSubmission, int, error) {
	where, args := "site_id = ?", []any{f.SiteID}
	if f.Form != "" {
		where += " AND form = ?"
		args = append(args, f.Form)
	}
	limit, offset := f.bounds()
	var out []domain.FormSubmission
	var total int
	err := db.withReadTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM form_submissions WHERE "+where, args...).Scan(&total); err != nil {
			return fmt.Errorf("count form submissions: %w", err)
		}
		rows, err := tx.QueryContext(ctx, "SELECT "+submissionCols+" FROM form_submissions WHERE "+where+ //nolint:gosec // G202: where is built from constants

			" ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?", append(args, limit, offset)...)
		if err != nil {
			return fmt.Errorf("list form submissions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			sub, err := scanSubmission(rows)
			if err != nil {
				return fmt.Errorf("scan form submission: %w", err)
			}
			out = append(out, sub)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("list form submissions: %w", err)
		}
		return attachDeliveries(ctx, tx, out)
	})
	return out, total, err
}

func attachDeliveries(ctx context.Context, q querier, subs []domain.FormSubmission) error {
	if len(subs) == 0 {
		return nil
	}
	ids := make([]string, len(subs))
	pos := map[int64]int{}
	for i, s := range subs {
		ids[i] = fmt.Sprint(s.ID)
		pos[s.ID] = i
	}
	rows, err := q.QueryContext(ctx, "SELECT "+deliveryCols+` FROM form_deliveries d
		JOIN form_channels c ON c.id = d.channel_id
		WHERE d.submission_id IN (`+strings.Join(ids, ",")+`) ORDER BY d.channel_id`)
	if err != nil {
		return fmt.Errorf("list form deliveries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return fmt.Errorf("scan form delivery: %w", err)
		}
		i := pos[d.SubmissionID]
		subs[i].Deliveries = append(subs[i].Deliveries, d)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list form deliveries: %w", err)
	}
	return nil
}

// ListFormNames returns the distinct form names of the site.
func (db *DB) ListFormNames(ctx context.Context, siteID string) ([]string, error) {
	rows, err := db.r.QueryContext(ctx, "SELECT DISTINCT form FROM form_submissions WHERE site_id = ? ORDER BY form", siteID)
	if err != nil {
		return nil, fmt.Errorf("list form names: %w", err)
	}
	return collectStrings(rows, "list form names")
}

// claimCandidatesSQL picks the oldest due delivery of every eligible
// channel and ranks them per site, so the result interleaves sites.
var claimCandidatesSQL = `WITH due AS (
		SELECT d.id, s.site_id, d.next_attempt_at,
			ROW_NUMBER() OVER (PARTITION BY d.channel_id ORDER BY d.next_attempt_at, d.id) AS ch_rank
		FROM form_deliveries d
		JOIN form_channels c ON c.id = d.channel_id
		JOIN form_submissions s ON s.id = d.submission_id
		WHERE d.status = 'pending' AND d.next_attempt_at <= ? AND c.paused_at IS NULL %s
	), heads AS (
		SELECT id, next_attempt_at,
			ROW_NUMBER() OVER (PARTITION BY site_id ORDER BY next_attempt_at, id) AS site_rank
		FROM due WHERE ch_rank = 1
	)
	SELECT ` + deliveryCols + ", " + jobChannelCols + `, s.id, s.site_id, s.host, s.form, s.fields_json, s.created_at, u.lang
	FROM heads h
	JOIN form_deliveries d ON d.id = h.id
	JOIN form_channels c ON c.id = d.channel_id
	JOIN form_submissions s ON s.id = d.submission_id
	JOIN sites st ON st.id = s.site_id
	JOIN users u ON u.id = st.owner_id
	ORDER BY h.site_rank, h.next_attempt_at, h.id LIMIT ?`

var jobChannelCols = prefixed("c", channelCols)

// cancelStaleSQL drops queued deliveries that must not be sent any more.
const cancelStaleSQL = `UPDATE form_deliveries SET status = 'cancelled', last_error = ?
	WHERE status = 'pending' AND channel_id IN (
		SELECT c.id FROM form_channels c JOIN sites st ON st.id = c.site_id
		WHERE c.enabled = 0 OR c.status <> 'active' OR st.deleted_at IS NOT NULL)`

// claimOverfetch is how many candidates are read per lease slot, so that
// candidates refused by Accept do not starve the claim.
const claimOverfetch = 4

// ClaimDueDeliveries cancels stale deliveries and leases due ones fairly.
func (db *DB) ClaimDueDeliveries(ctx context.Context, c DeliveryClaim) ([]DeliveryJob, error) {
	if c.Limit <= 0 {
		return nil, nil
	}
	skip := ""
	if len(c.SkipChannels) > 0 {
		ids := make([]string, len(c.SkipChannels))
		for i, id := range c.SkipChannels {
			ids[i] = strconv.FormatInt(id, 10)
		}
		skip = "AND d.channel_id NOT IN (" + strings.Join(ids, ",") + ")"
	}
	var jobs []DeliveryJob
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, cancelStaleSQL, DeliveryCancelledMsg); err != nil {
			return fmt.Errorf("cancel stale deliveries: %w", err)
		}
		candidates, err := queryJobs(ctx, tx, fmt.Sprintf(claimCandidatesSQL, skip), toMS(c.Now), c.Limit*claimOverfetch)
		if err != nil {
			return err
		}
		for _, j := range candidates {
			if len(jobs) == c.Limit {
				break
			}
			if c.Accept != nil && !c.Accept(j) {
				continue
			}
			if _, err := tx.ExecContext(ctx, "UPDATE form_deliveries SET next_attempt_at = ? WHERE id = ?",
				toMS(c.LeaseUntil), j.Delivery.ID); err != nil {
				return fmt.Errorf("lease delivery: %w", err)
			}
			jobs = append(jobs, j)
		}
		return nil
	})
	return jobs, err
}

func queryJobs(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]DeliveryJob, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("claim deliveries: %w", err)
	}
	defer rows.Close()
	var out []DeliveryJob
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("claim deliveries: %w", err)
	}
	return out, nil
}

func scanJob(rows *sql.Rows) (DeliveryJob, error) {
	var j DeliveryJob
	var next int64
	var sent, expires, paused sql.NullInt64
	var created, updated, subCreated int64
	var fields string
	d, c, s := &j.Delivery, &j.Channel, &j.Submission
	err := rows.Scan(&d.ID, &d.SubmissionID, &d.ChannelID, &d.ChannelKind, &d.ChannelTarget, &d.Status,
		&d.Attempts, &next, &d.LastError, &sent,
		&c.ID, &c.SiteID, &c.Kind, &c.Target, &c.SecretEnc, &c.Status, &c.Enabled, &c.VerifyHash,
		&expires, &c.VerifyAttempts, &c.ConsecutiveFailures, &paused, &created, &updated,
		&s.ID, &s.SiteID, &s.Host, &s.Form, &fields, &subCreated, &j.OwnerLang)
	if err != nil {
		return DeliveryJob{}, fmt.Errorf("scan delivery job: %w", err)
	}
	if err := json.Unmarshal([]byte(fields), &s.Fields); err != nil {
		return DeliveryJob{}, fmt.Errorf("decode form fields: %w", err)
	}
	d.NextAttemptAt, d.SentAt = fromMS(next), fromNullMS(sent)
	c.VerifyExpiresAt, c.PausedAt = fromNullMS(expires), fromNullMS(paused)
	c.CreatedAt, c.UpdatedAt, s.CreatedAt = fromMS(created), fromMS(updated), fromMS(subCreated)
	return j, nil
}

// prefixed qualifies a comma-separated column list with a table alias.
func prefixed(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// FinishDelivery stores the outcome of an attempt.
func (db *DB) FinishDelivery(ctx context.Context, d domain.FormDelivery) error {
	res, err := db.w.ExecContext(ctx, `UPDATE form_deliveries SET status = ?, attempts = ?, next_attempt_at = ?,
		last_error = ?, sent_at = ? WHERE id = ?`, string(d.Status), d.Attempts, toMS(d.NextAttemptAt),
		d.LastError, nullMS(d.SentAt), d.ID)
	return expectAffected(res, err, "finish delivery")
}

// UseAltchaSolution records a solution; false means it was already used.
func (db *DB) UseAltchaSolution(ctx context.Context, signature string, expires time.Time) (bool, error) {
	res, err := db.w.ExecContext(ctx, `INSERT INTO form_altcha_used (signature, expires_at) VALUES (?, ?)
		ON CONFLICT (signature) DO NOTHING`, signature, toMS(expires))
	n, err := affected(res, err, "use altcha solution")
	return n == 1, err
}

// PurgeForms applies retention.
func (db *DB) PurgeForms(ctx context.Context, cutoff, now time.Time) (int, error) {
	var n int
	err := db.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM form_submissions WHERE created_at < ?
			OR site_id IN (SELECT id FROM sites WHERE deleted_at IS NOT NULL)`, toMS(cutoff))
		if n, err = affected(res, err, "purge form submissions"); err != nil {
			return err
		}
		for _, q := range []string{
			"DELETE FROM form_channels WHERE site_id IN (SELECT id FROM sites WHERE deleted_at IS NOT NULL)",
			"DELETE FROM form_site_settings WHERE site_id IN (SELECT id FROM sites WHERE deleted_at IS NOT NULL)",
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return fmt.Errorf("purge forms of deleted sites: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM form_altcha_used WHERE expires_at < ?", toMS(now)); err != nil {
			return fmt.Errorf("purge altcha records: %w", err)
		}
		return nil
	})
	return n, err
}
