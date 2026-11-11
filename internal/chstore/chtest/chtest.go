// Package chtest gives each test a fresh, migrated ClickHouse database.
// Tests using it are skipped unless TAKTAPLUS_TEST_CLICKHOUSE_URL is set.
package chtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/farshadfighter/taktaplus/internal/chstore"
)

// New returns a client bound to a new database that is dropped when the test
// ends.
func New(t *testing.T) *chstore.Client {
	t.Helper()
	url := os.Getenv("TAKTAPLUS_TEST_CLICKHOUSE_URL")
	if url == "" {
		t.Skip("TAKTAPLUS_TEST_CLICKHOUSE_URL not set")
	}
	ctx := context.Background()
	admin, err := chstore.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	db := "t_" + hex.EncodeToString(b)
	if err := admin.Exec(ctx, "CREATE DATABASE "+db, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+db+" SYNC", nil) })
	c := admin.WithDatabase(db)
	if err := c.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	return c
}
