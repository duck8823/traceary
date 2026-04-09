package sqlite

import (
	"context"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // Required to register the SQLite driver.

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
)

// Datasource is a SQLite-backed data source.
type Datasource struct {
	migrations fs.FS
}

var _ usecase.StoreInitializer = (*Datasource)(nil)

// NewDatasource creates a new Datasource.
func NewDatasource(migrations fs.FS) *Datasource {
	return &Datasource{migrations: migrations}
}

// Initialize initializes the SQLite store.
func (d *Datasource) Initialize(ctx context.Context, dbPath string) (err error) {
	trimmedPath := strings.TrimSpace(dbPath)
	if trimmedPath == "" {
		return xerrors.Errorf("DB path must not be empty")
	}

	if err := os.MkdirAll(filepath.Dir(trimmedPath), 0o700); err != nil {
		return xerrors.Errorf("failed to create DB directory: %w", err)
	}

	db, err := d.openDB(ctx, trimmedPath)
	if err != nil {
		return xerrors.Errorf("failed to initialize SQLite connection: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("failed to close SQLite connection: %w", closeErr)
		}
	}()
	if chmodErr := os.Chmod(trimmedPath, 0o600); chmodErr != nil && !os.IsPermission(chmodErr) {
		return xerrors.Errorf("failed to set SQLite DB file permissions: %w", chmodErr)
	}

	if err := d.migrate(ctx, db); err != nil {
		return xerrors.Errorf("failed to run SQLite migrations: %w", err)
	}

	return nil
}

func sqliteDSN(dbPath string) string {
	values := url.Values{}
	values.Add("_pragma", "foreign_keys(1)")

	return (&url.URL{
		Scheme:   "file",
		Path:     dbPath,
		RawQuery: values.Encode(),
	}).String()
}
