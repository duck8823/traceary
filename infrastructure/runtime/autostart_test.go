//go:build darwin || linux

package runtime_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

func TestAutoStartOperatorMissingReadsNeverCreateRoot(t *testing.T) {
	root := filepath.Join(daemonRoot(t), "missing")
	_, err := (infra.LocalOperatorFactory{}).OpenAutoStart(context.Background(), root)
	if err == nil {
		t.Fatal("missing root accepted")
	}
	if _, err = os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created missing root", err)
	}
}
func TestAutoStartOperatorRejectsUnsafeRootsBeforeCreation(t *testing.T) {
	base := daemonRoot(t)
	unsafe := filepath.Join(base, "public")
	if err := os.Mkdir(unsafe, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(base, link); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"relative", base + "/../other", base + "/", filepath.Join(base, strings.Repeat("x", 110)), base + "\x01", base + "\xff", filepath.Join(link, "child"), unsafe} {
		t.Run("refuse", func(t *testing.T) {
			if _, err := (infra.LocalOperatorFactory{}).CreateAutoStart(context.Background(), root); err == nil {
				t.Fatal("unsafe root accepted")
			}
		})
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 2 {
		t.Fatal("unsafe root refusal wrote files", err, entries)
	}
}
func TestAutoStartOperatorPreferencePreservesEpochAndContinuousUpdate(t *testing.T) {
	root := filepath.Join(daemonRoot(t), "new")
	ctx := context.Background()
	service, err := (infra.LocalOperatorFactory{}).CreateAutoStart(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(ctx)
	if err != nil || status.OptIn {
		t.Fatal(status, err)
	}
	store, err := infra.OpenExecutionStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	epoch := strings.Repeat("a", 32)
	if _, err = store.RecordEpochStart(ctx, epoch); err != nil {
		t.Fatal(err)
	}
	deferral, err := domain.NewUpdateDeferral(domain.UpdateTarget{Version: "next", Protocol: 2}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveUpdateDeferral(ctx, epoch, deferral.Snapshot()); err != nil {
		t.Fatal(err)
	}
	for _, value := range []bool{true, false} {
		if _, err = service.SetOptIn(ctx, value); err != nil {
			t.Fatal(err)
		}
		state, err := store.ReadLifecycle(ctx)
		update, found := state.UpdateDeferral.Value()
		if err != nil || state.Epoch != epoch || state.Clean || state.AutoStartOptIn != value || !found || update != deferral.Snapshot() {
			t.Fatal("preference corrupted lifecycle", err, state)
		}
	}
	// Independent descriptors share the journal lock. Preference changes may
	// neither erase a concurrently published target nor reset the epoch marker.
	var workers sync.WaitGroup
	for i := range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if i%2 == 0 {
				other, err := (infra.LocalOperatorFactory{}).OpenAutoStart(ctx, root)
				if err != nil {
					t.Error(err)
					return
				}
				if _, err = other.SetOptIn(ctx, true); err != nil {
					t.Error(err)
				}
			} else if err := store.SaveUpdateDeferral(ctx, epoch, deferral.Snapshot()); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	state, err := store.ReadLifecycle(ctx)
	update, found := state.UpdateDeferral.Value()
	if err != nil || state.Epoch != epoch || !state.AutoStartOptIn || !found || update != deferral.Snapshot() {
		t.Fatal("concurrent preference erased lifecycle", err)
	}
	if _, err = service.SetOptIn(ctx, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"execution.json", "registry.json", "daemon.sock", "observations.sock", "core.db"} {
		if _, err = os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("preference created unrelated state", name, err)
		}
	}
	if _, err = service.Descriptor(ctx); !errors.Is(err, domain.ErrUnsafe) {
		t.Fatal("descriptor bypassed opt-in", err)
	}
}
