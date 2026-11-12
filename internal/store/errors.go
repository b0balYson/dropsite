package store

import (
	"database/sql"
	"errors"
	"fmt"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Domain errors produced by the store.
var (
	errEmailTaken    = domain.E(domain.KindConflict, domain.CodeEmailTaken, "email is already registered")
	errSlugTaken     = domain.E(domain.KindConflict, domain.CodeSlugTaken, "address is already taken")
	errInviteInvalid = domain.E(domain.KindInvalid, domain.CodeInviteInvalid, "invite link is invalid, used or expired")
)

// isUniqueViolation reports whether err is a UNIQUE or PRIMARY KEY constraint
// failure, judged by the driver's extended result code.
func isUniqueViolation(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	switch se.Code() {
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
		return true
	}
	return false
}

// mapUnique turns a unique violation into conflict and wraps anything else with op.
func mapUnique(err error, conflict *domain.Error, op string) error {
	if isUniqueViolation(err) {
		return conflict.WithCause(err)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// mapNoRows turns sql.ErrNoRows into domain.ErrNotFound and wraps anything else with op.
func mapNoRows(err error, op string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	return fmt.Errorf("%s: %w", op, err)
}

// expectAffected returns domain.ErrNotFound when an UPDATE or DELETE matched no row.
func expectAffected(res sql.Result, err error, op string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: rows affected: %w", op, err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// affected returns the number of rows changed by a statement.
func affected(res sql.Result, err error, op string) (int, error) {
	if err != nil {
		return 0, fmt.Errorf("%s: %w", op, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("%s: rows affected: %w", op, err)
	}
	return int(n), nil
}
