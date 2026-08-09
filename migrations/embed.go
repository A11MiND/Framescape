// Package migrations embeds the goose SQL migration files (same pattern as
// workflows/embed.go), so cmd/migrate doesn't need a `goose` CLI or a
// filesystem-mounted migrations directory in the Docker image — the SQL
// ships inside the binary itself.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
