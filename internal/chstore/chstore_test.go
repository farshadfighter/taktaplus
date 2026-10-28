package chstore_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/farshadfighter/taktaplus/internal/chstore"
	"github.com/farshadfighter/taktaplus/internal/chstore/chtest"
)

func TestMigrateInsertRollupRetention(t *testing.T) {
	c := chtest.New(t)
	ctx := context.Background()
	// Migrating again is a no-op.
	if err := c.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Hour).Add(10 * time.Minute)
	row := func(src string, sent uint64) any {
		return map[string]any{"ts": now.Format(time.RFC3339Nano), "device_id": 7, "srcip": src, "app": "HTTPS",
			"sentbyte": sent, "rcvdbyte": 10, "action": "accept", "fields": map[string]string{"srcip": src, "note": "it's"}}
	}
	if err := c.Insert(ctx, "log_traffic", []any{row("10.0.0.1", 100), row("10.0.0.1", 50), row("10.0.0.2", 5)}); err != nil {
		t.Fatal(err)
	}
	type top struct {
		SrcIP string `json:"srcip"`
		Sent  uint64 `json:"sent"`
		N     uint64 `json:"sessions"`
	}
	got, err := chstore.Select[top](ctx, c, `SELECT srcip, sum(sent) AS sent, sum(sessions) AS sessions FROM r_traffic_src
		WHERE device_id IN {devs:Array(UInt32)} AND hour >= {from:DateTime64(3)} GROUP BY srcip ORDER BY sent DESC`,
		chstore.Params{"devs": []uint32{7}, "from": now.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SrcIP != "10.0.0.1" || got[0].Sent != 150 || got[0].N != 2 {
		t.Fatalf("rollup = %+v", got)
	}
	// String parameters are never parsed as SQL.
	type cnt struct {
		N uint64 `json:"n"`
	}
	n, err := chstore.Select[cnt](ctx, c, `SELECT count() AS n FROM log_traffic WHERE fields['note'] = {v:String}`, chstore.Params{"v": "it's"})
	if err != nil || n[0].N != 3 {
		t.Fatalf("param query = %+v, %v", n, err)
	}
	if err := c.SetRetention(ctx, chstore.Retention{RawDays: 30, RollupDays: 180}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRetention(ctx, chstore.Retention{RawDays: 30, RollupDays: 10}); err == nil {
		t.Fatal("rollups shorter than raw logs accepted")
	}
	u, err := c.Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range u {
		if x.Table == "log_traffic" && x.Rows == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("usage = %+v", u)
	}
	// Errors from the server surface.
	if err := c.Exec(ctx, "SELECT nope FROM missing_table", nil); err == nil {
		t.Fatal("error not reported")
	}
}
