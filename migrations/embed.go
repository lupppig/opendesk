// Package migrations embeds the SQL migrations. Add a new pair numbered above
// the highest one on main: 000123_name.up.sql and 000123_name.down.sql. A lower
// number is never applied to a database that is already past it
// (scripts/check-migrations.sh).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
