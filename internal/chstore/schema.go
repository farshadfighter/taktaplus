package chstore

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/farshadfighter/taktaplus/migrations"
)

// Raw log tables and hourly rollup tables.
var (
	RawTables    = []string{"log_traffic", "log_threat", "log_event", "log_waf"}
	RollupTables = []string{"r_traffic_src", "r_traffic_dst", "r_traffic_app", "r_threat", "r_web", "r_vpn", "r_event", "r_waf"}
)

// Migrate applies the pending ClickHouse migrations.
func (c *Client) Migrate(ctx context.Context, log *slog.Logger) error {
	if err := c.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version UInt32, applied_at DateTime DEFAULT now()) ENGINE = MergeTree ORDER BY version`, nil); err != nil {
		return fmt.Errorf("chstore: migrations table: %w", err)
	}
	type row struct {
		Version uint32 `json:"version"`
	}
	done, err := Select[row](ctx, c, "SELECT version FROM schema_migrations", nil)
	if err != nil {
		return err
	}
	applied := map[uint32]bool{}
	for _, r := range done {
		applied[r.Version] = true
	}
	files, err := fs.Glob(migrations.ClickHouse, "clickhouse/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		base := f[strings.LastIndex(f, "/")+1:]
		n, err := strconv.ParseUint(strings.SplitN(base, "_", 2)[0], 10, 32)
		if err != nil {
			return fmt.Errorf("chstore: migration %s: bad version prefix", base)
		}
		if applied[uint32(n)] {
			continue
		}
		data, err := fs.ReadFile(migrations.ClickHouse, f)
		if err != nil {
			return err
		}
		for _, stmt := range splitStatements(string(data)) {
			if err := c.Exec(ctx, stmt, nil); err != nil {
				return fmt.Errorf("chstore: migration %s: %w", base, err)
			}
		}
		if err := c.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ({v:UInt32})", Params{"v": n}); err != nil {
			return err
		}
		log.Info("applied clickhouse migration", "file", base)
	}
	return nil
}

// splitStatements drops comment lines and splits on semicolons that end a
// line.
func splitStatements(sql string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "--") {
			continue
		}
		cur.WriteString(line)
		cur.WriteByte('\n')
		if strings.HasSuffix(t, ";") {
			out = append(out, strings.TrimSuffix(strings.TrimSpace(cur.String()), ";"))
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// Retention is how long logs are kept, in days.
type Retention struct {
	RawDays    int `json:"raw_days"`
	RollupDays int `json:"rollup_days"`
}

// DefaultRetention keeps raw logs 90 days and hourly rollups a year.
var DefaultRetention = Retention{RawDays: 90, RollupDays: 365}

// Validate checks the bounds.
func (r Retention) Validate() error {
	if r.RawDays < 1 || r.RawDays > 3650 || r.RollupDays < 1 || r.RollupDays > 3650 {
		return fmt.Errorf("retention must be between 1 and 3650 days")
	}
	if r.RollupDays < r.RawDays {
		return fmt.Errorf("summaries must be kept at least as long as raw logs")
	}
	return nil
}

// SetRetention changes the TTL of every table. Old data beyond the new limit
// is removed in the background.
func (c *Client) SetRetention(ctx context.Context, r Retention) error {
	if err := r.Validate(); err != nil {
		return err
	}
	for _, t := range RawTables {
		if err := c.Exec(ctx, fmt.Sprintf("ALTER TABLE %s MODIFY TTL toDateTime(ts) + INTERVAL %d DAY DELETE", t, r.RawDays), nil); err != nil {
			return err
		}
	}
	for _, t := range RollupTables {
		if err := c.Exec(ctx, fmt.Sprintf("ALTER TABLE %s MODIFY TTL hour + INTERVAL %d DAY DELETE", t, r.RollupDays), nil); err != nil {
			return err
		}
	}
	return nil
}

// TableUsage is the disk use of one table.
type TableUsage struct {
	Table string `json:"table"`
	Rows  uint64 `json:"rows"`
	Bytes uint64 `json:"bytes"`
	// Oldest is the earliest day still stored (empty when the table is empty).
	Oldest string `json:"oldest"`
}

// Usage reports disk use per log table.
func (c *Client) Usage(ctx context.Context) ([]TableUsage, error) {
	return Select[TableUsage](ctx, c, `SELECT table, sum(rows) AS rows, sum(bytes_on_disk) AS bytes,
		toString(toDate(min(if(min_time > 0, min_time, toDateTime(min_date))))) AS oldest
		FROM system.parts WHERE database = currentDatabase() AND active AND (table LIKE 'log\_%' OR table LIKE 'r\_%')
		GROUP BY table ORDER BY table`, nil)
}
