//go:build darwin || linux

package sqlite

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"golang.org/x/sys/unix"
)

func TestHistoryMaintenanceMarkerRefusesFIFOAndSymlinkWithoutReadingThem(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink", "oversize", "malformed", "hardlink", "overflow-pid"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store.db")
			if err := os.WriteFile(path, []byte("store"), 0600); err != nil {
				t.Fatal(err)
			}
			marker, err := CompactPendingMarkerPath(path)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "fifo":
				err = unix.Mkfifo(marker, 0600)
			case "symlink":
				err = os.Symlink(path, marker)
			case "oversize":
				err = os.WriteFile(marker, make([]byte, 1024), 0600)
			case "malformed":
				err = os.WriteFile(marker, []byte("not a pid"), 0600)
			case "overflow-pid":
				err = os.WriteFile(marker, []byte("4294967296"), 0600)
			case "hardlink":
				err = os.Link(path, marker)
			}
			if err != nil {
				t.Fatal(err)
			}
			var pending *apptypes.StoreMaintenancePendingError
			if err = consultHistoryMaintenanceMarker(path); !errors.As(err, &pending) {
				t.Fatal("unsafe marker read as idle", err)
			}
			if _, err = os.Lstat(marker); err != nil {
				t.Fatal("history removed marker", err)
			}
		})
	}
}

func TestHistoryMaintenanceMarkerStalePIDDoesNotDeleteLegacyMarker(t *testing.T) {
	// Only a finite test-owned child is launched; no native host/agent or user PID.
	child := exec.Command(os.Args[0], "-test.run=^$")
	if err := child.Run(); err != nil {
		t.Fatal(err)
	}
	pid := child.Process.Pid
	if err := unix.Kill(pid, 0); !errors.Is(err, unix.ESRCH) {
		t.Skip("test-owned child PID already reused; do not infer stale ownership")
	}
	d, path := historyReadFixture(t, supportedHistoryReadSchema)
	marker, err := CompactPendingMarkerPath(path)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(strconv.Itoa(pid) + "\n")
	if err = os.WriteFile(marker, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = d.WithYieldingReadScope(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal("known stale marker prevented history read", err)
	}
	after, err := os.ReadFile(marker)
	if err != nil || string(data) != string(after) {
		t.Fatal("history reader reaped a marker owned by legacy maintenance", err)
	}
}

func TestHistoryMaintenanceMarkerAppearingBeforeSharedAcquireRefusesReader(t *testing.T) {
	d, path := historyReadFixture(t, supportedHistoryReadSchema)
	defer clearCompactPendingMarker(path)
	afterMaintenanceMarkerConsult = func() {
		if err := writeCompactPendingMarker(path); err != nil {
			t.Error(err)
		}
	}
	defer func() { afterMaintenanceMarkerConsult = nil }()
	if err := d.WithYieldingReadScope(context.Background(), func(context.Context) error { t.Fatal("reader admitted after maintenance marker"); return nil }); err == nil {
		t.Fatal("marker race bypassed admission")
	}
}

func TestHistoryReadExpiredDeadlineIsBusyWithoutEnteringScope(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	err := NewDatabase(filepath.Join(t.TempDir(), "missing.db"), nil).WithYieldingReadScope(ctx, func(context.Context) error { t.Fatal("expired read started"); return nil })
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, queryservice.ErrHistoryReadBusy) {
		t.Fatal(err)
	}
}
