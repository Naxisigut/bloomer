// Package migrations embeds immutable, versioned SQL migrations in the binary.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
