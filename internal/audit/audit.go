// Package audit records who did what. Entries are append-only.
package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sources of audited actions.
const (
	SourcePanel  = "panel"
	SourceAPI    = "api"
	SourceSystem = "system"
)

// Entry is one audited action.
type Entry struct {
	ID         int64          `json:"id"`
	At         time.Time      `json:"at"`
	Source     string         `json:"source"`
	ActorID    *int64         `json:"actor_id,omitempty"`
	Actor      string         `json:"actor"`
	IP         string         `json:"ip"`
	Action     string         `json:"action"`
	ObjectType string         `json:"object_type"`
	ObjectID   string         `json:"object_id"`
	Details    map[string]any `json:"details"`
}

// Execer is satisfied by a pool and by a transaction, so an entry can be
// written in the same transaction as the change it records.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Log writes an entry.
func Log(ctx context.Context, db Execer, e Entry) error {
	details, err := json.Marshal(nonNil(e.Details))
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
		INSERT INTO audit_log (source, actor_id, actor, ip, action, object_type, object_id, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		e.Source, e.ActorID, e.Actor, e.IP, e.Action, e.ObjectType, e.ObjectID, details)
	return err
}

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// List returns the most recent entries, newest first.
func List(ctx context.Context, db *pgxpool.Pool, limit int) ([]Entry, error) {
	rows, err := db.Query(ctx, `
		SELECT id, at, source, actor_id, actor, ip, action, object_type, object_id, details
		FROM audit_log ORDER BY at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.At, &e.Source, &e.ActorID, &e.Actor, &e.IP, &e.Action, &e.ObjectType, &e.ObjectID, &e.Details); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
