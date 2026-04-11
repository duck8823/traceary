package sqlite

import (
	"context"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // Required to register the SQLite driver.

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/port"
)

// Datasource is a SQLite-backed data source.
type Datasource struct {
	dbPath     string
	migrations fs.FS
}

var _ port.StoreInitializer = (*Datasource)(nil)

// NewDatasource creates a new Datasource bound to the given database path.
func NewDatasource(dbPath string, migrations fs.FS) *Datasource {
	return &Datasource{dbPath: strings.TrimSpace(dbPath), migrations: migrations}
}

// Initialize initializes the SQLite store.
func (d *Datasource) Initialize(ctx context.Context) (err error) {
	trimmedPath := d.dbPath
	if trimmedPath == "" {
		return xerrors.Errorf("DB path must not be empty")
	}

	if err := os.MkdirAll(filepath.Dir(trimmedPath), 0o700); err != nil {
		return xerrors.Errorf("failed to create DB directory: %w", err)
	}

	db, err := d.openDB(ctx)
	if err != nil {
		return xerrors.Errorf("failed to initialize SQLite connection: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("failed to close SQLite connection: %w", closeErr)
		}
	}()
	// chmod is best-effort; ignore errors on read-only filesystems or
	// when the DB file is owned by another user.
	if chmodErr := os.Chmod(trimmedPath, 0o600); chmodErr != nil {
		slog.Debug("failed to set DB file permissions (best-effort)", "error", chmodErr)
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
