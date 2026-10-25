// Package migrations embeds the SQL schema migrations (goose format).
package migrations

import "embed"

// Postgres holds the PostgreSQL migrations.
//
//go:embed postgres/*.sql
var Postgres embed.FS
