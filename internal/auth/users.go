package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrUserNotFound is returned for unknown user ids.
var ErrUserNotFound = errors.New("user not found")

// UserInfo is a user row for administration screens.
type UserInfo struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	Name        string     `json:"name"`
	Email       string     `json:"email"`
	Phone       string     `json:"phone"`
	Role        Role       `json:"role"`
	Source      string     `json:"source"`
	TOTPEnabled bool       `json:"totp_enabled"`
	Active      bool       `json:"active"`
	Locked      bool       `json:"locked"`
	LastLoginAt *time.Time `json:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// ListUsers returns all panel users.
func (s *Service) ListUsers(ctx context.Context) ([]UserInfo, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, username, name, email, phone, role, source, totp_enabled, active,
		       coalesce(locked_until > now(), false), last_login_at, created_at
		FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserInfo{}
	for rows.Next() {
		var u UserInfo
		if err := rows.Scan(&u.ID, &u.Username, &u.Name, &u.Email, &u.Phone, &u.Role, &u.Source, &u.TOTPEnabled, &u.Active, &u.Locked, &u.LastLoginAt, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UserUpdate holds editable user fields.
type UserUpdate struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	Phone  string `json:"phone"`
	Role   Role   `json:"role"`
	Active bool   `json:"active"`
}

// UpdateUser changes a user's profile, role and active flag. A user cannot
// demote or deactivate themselves, so a panel never loses its last admin by
// accident.
func (s *Service) UpdateUser(ctx context.Context, actorID, id int64, u UserUpdate) error {
	if !u.Role.Valid() {
		return fmt.Errorf("unknown role %q", u.Role)
	}
	if actorID == id {
		var cur string
		if err := s.db.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, id).Scan(&cur); err != nil {
			return err
		}
		if Role(cur) != u.Role || !u.Active {
			return errors.New("you cannot change your own role or deactivate yourself")
		}
	}
	tag, err := s.db.Exec(ctx, `UPDATE users SET name = $2, email = $3, phone = $4, role = $5, active = $6 WHERE id = $1`,
		id, strings.TrimSpace(u.Name), strings.TrimSpace(u.Email), strings.TrimSpace(u.Phone), string(u.Role), u.Active)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	if !u.Active {
		_, err = s.db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id)
	}
	return err
}

// SetPassword replaces a user's password and ends their sessions.
func (s *Service) SetPassword(ctx context.Context, id int64, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, failed_logins = 0, locked_until = NULL WHERE id = $1`, id, hash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrUserNotFound
		}
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id)
		return err
	})
}

// ResetMFA forces the user to enrol a new authenticator at next login.
func (s *Service) ResetMFA(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE users SET totp_enabled = false, totp_secret = NULL WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrUserNotFound
		}
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id)
		return err
	})
}

// Unlock clears a lockout after failed logins.
func (s *Service) Unlock(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET failed_logins = 0, locked_until = NULL WHERE id = $1`, id)
	return err
}

func (s *Service) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
