// Package settings stores panel settings sealed with the master key.
package settings

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus/internal/secretbox"
)

// Store reads and writes settings.
type Store struct {
	db  *pgxpool.Pool
	box *secretbox.Box
}

// New returns a store.
func New(db *pgxpool.Pool, box *secretbox.Box) *Store { return &Store{db: db, box: box} }

func ctxOf(key string) string { return "setting:" + key }

// Get decodes the setting into v; a missing setting leaves v unchanged.
func (s *Store) Get(ctx context.Context, key string, v any) error {
	var sealed []byte
	err := s.db.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	plain, err := s.box.Open(sealed, ctxOf(key))
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, v)
}

// Set stores v.
func (s *Store) Set(ctx context.Context, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, s.box.Seal(data, ctxOf(key)))
	return err
}
