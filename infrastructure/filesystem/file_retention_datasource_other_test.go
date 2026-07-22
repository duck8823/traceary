//go:build !unix

package filesystem

import (
	"context"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestFileRetentionInventoryFailsClosedWhenRootEvidenceIsUnsupported(t *testing.T) {
	t.Parallel()

	snapshot, err := NewFileRetentionDatasource().InspectFileRetention(context.Background(), apptypes.FileRetentionInventoryRequest{
		Class: "backup", Root: `C:\backups`, DatabasePath: `C:\traceary.db`,
	})
	if err == nil || !strings.Contains(err.Error(), "requires Unix") {
		t.Fatalf("InspectFileRetention() error = %v, want unsupported-platform failure", err)
	}
	if snapshot.RootAccess.ApplyState != "" {
		t.Fatalf("snapshot = %#v, want no fabricated root evidence", snapshot)
	}
}
