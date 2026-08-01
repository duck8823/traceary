// Package sqlite embeds the canonical production SQLite migrations.
package sqlite

import (
	"embed"
	"io/fs"

	"golang.org/x/xerrors"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations returns the canonical production migrations rooted at their filenames.
func Migrations() (fs.FS, error) {
	migrations, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, xerrors.Errorf("open embedded migrations: %w", err)
	}
	return migrations, nil
}
