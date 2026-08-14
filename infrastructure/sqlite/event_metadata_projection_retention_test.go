package sqlite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEventMetadataProjectionReadersRemain documents #1686: do not switch
// metadata reads back to events while events.body is still inline (#1743).
func TestEventMetadataProjectionReadersRemain(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir("sql")
	if err != nil {
		t.Fatalf("read sql/: %v", err)
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join("sql", entry.Name()))
		if readErr != nil {
			t.Fatalf("read %s: %v", entry.Name(), readErr)
		}
		if strings.Contains(string(body), "event_metadata_projection") {
			files = append(files, entry.Name())
		}
	}
	if len(files) < 20 {
		t.Fatalf("projection readers = %d (%v), want at least the 20 files #1686 inventoried", len(files), files)
	}
}
