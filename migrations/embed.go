// Package migrations embeds the SQL schema migrations.
package migrations

import "embed"

// Postgres holds the PostgreSQL migrations (goose format).
//
//go:embed postgres/*.sql
var Postgres embed.FS

// ClickHouse holds the log store migrations, applied in file-name order by
// chstore.Migrate.
//
//go:embed clickhouse/*.sql
var ClickHouse embed.FS
