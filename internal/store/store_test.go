package store

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
)

// Requires a disposable database: TAKTAPLUS_TEST_DATABASE_URL (set in CI).
func TestMigrate(t *testing.T) {
	url := os.Getenv("TAKTAPLUS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TAKTAPLUS_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for i := 0; i < 2; i++ { // second run must be a no-op
		if err := Migrate(ctx, pool, log); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM app_meta").Scan(&n); err != nil {
		t.Fatal(err)
	}
}
