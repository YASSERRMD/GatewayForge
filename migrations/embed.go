package migrations

import "embed"

// Files embeds all ordered SQL up-migrations.
//
//go:embed *.sql
var Files embed.FS
