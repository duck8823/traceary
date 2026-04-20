package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestSQLiteDSN_IncludesConcurrencyPragmas asserts that sqliteDSN emits
// the journal_mode, synchronous, busy_timeout, and foreign_keys pragmas
// so readers and writers can proceed concurrently under hook load.
func TestSQLiteDSN_IncludesConcurrencyPragmas(t *testing.T) {
	t.Parallel()

	dsn := sqliteDSN("/tmp/example.db")

	// url.Values URL-encodes parentheses to %28 and %29 when building
	// the query string.
	wants := []string{
		"journal_mode%28WAL%29",
		"synchronous%28NORMAL%29",
		"busy_timeout%285000%29",
		"foreign_keys%281%29",
	}
	for _, want := range wants {
		if !strings.Contains(dsn, want) {
			t.Errorf("sqliteDSN(%q) = %q; missing pragma %q", "/tmp/example.db", dsn, want)
		}
	}
}

// TestSQLiteDSN_AppliesPragmasOnOpen opens a real SQLite connection via
// the DSN and verifies that the journal_mode, synchronous, and
// busy_timeout pragmas actually take effect on the resulting connection.
func TestSQLiteDSN_AppliesPragmasOnOpen(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")

	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	})
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}

	tests := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"synchronous", "1"}, // NORMAL
		{"busy_timeout", "5000"},
		{"foreign_keys", "1"},
	}
	for _, tt := range tests {
		var got string
		if err := db.QueryRowContext(context.Background(), "PRAGMA "+tt.pragma).Scan(&got); err != nil {
			t.Errorf("PRAGMA %s error = %v", tt.pragma, err)
			continue
		}
		if got != tt.want {
			t.Errorf("PRAGMA %s = %q; want %q", tt.pragma, got, tt.want)
		}
	}
}
