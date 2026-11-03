//go:build darwin || linux

package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// No launch is permitted in this real socket/private-journal timer fixture.
type updateNoLaunchPorts struct{ calls atomic.Int32 }

func (*updateNoLaunchPorts) Resolve(context.Context, domain.WorktreeID, string) (domain.Target, error) {
	return domain.Target{}, domain.ErrUnsupported
}
func (*updateNoLaunchPorts) Revalidate(context.Context, domain.Target, string) error {
	return domain.ErrUnsupported
}
func (p *updateNoLaunchPorts) Start(context.Context, domain.LaunchDescriptor) (app.OwnedChild, error) {
	p.calls.Add(1)
	return nil, domain.ErrUnsupported
}

func TestDaemonUpdateTimerRestoresAndPublishesOverduePrivateNoticeWithoutAgents(t *testing.T) {
	ctx := context.Background()
	root := receiptRoot(t)
	journal, err := NewExecutionStore(root, MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	oldEpoch := strings.Repeat("a", 32)
	if _, err = journal.RecordEpochStart(ctx, oldEpoch); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Round(0).Add(-25 * time.Hour)
	state := domain.UpdateDeferralState{Target: domain.UpdateTarget{Version: "v2", Protocol: 2}, Started: start}
	if err = journal.SaveUpdateDeferral(ctx, oldEpoch, state); err != nil {
		t.Fatal(err)
	}
	ports := &updateNoLaunchPorts{}
	owners := make(chan *app.Runtime, 1)
	d, err := NewLocalDaemon(nil, DaemonOptions{RuntimeFactory: func(ctx context.Context, path, epoch string) (*app.Runtime, error) {
		store, e := OpenExecutionStore(path, MaxRegistryBytes)
		if e != nil {
			return nil, e
		}
		owner, e := app.NewRuntime(ports, store, ports, store, epoch)
		if e != nil {
			return nil, fmt.Errorf("construct notice fixture owner: %w", e)
		}
		if _, e = owner.InitializeLifecycle(ctx); e != nil {
			return nil, fmt.Errorf("initialize notice fixture owner: %w", e)
		}
		owners <- owner
		return owner, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	epoch := serveContinuityFixture(t, d, root)
	owner := <-owners
	deadline := time.Now().Add(3 * time.Second)
	published := false
	for time.Now().Before(deadline) {
		snapshot, e := journal.ReadLifecycle(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if current, found := snapshot.UpdateDeferral.Value(); found && current.Notified24h {
			if snapshot.Epoch != epoch || !current.Started.Equal(start) || current.Target != state.Target {
				t.Fatal("restart/timer reset continuous deferral")
			}
			published = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !published {
		t.Fatal("foreground24h metadata notice was not published")
	}
	before, err := os.ReadFile(filepath.Join(root, "lifecycle.json"))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		status, e := owner.UpdateStatus(ctx)
		if e != nil || !status.Overdue24h {
			t.Fatal("online status consumed overdue notice", e)
		}
	}
	after, err := os.ReadFile(filepath.Join(root, "lifecycle.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("status rewrote durable notice", err)
	}
	if ports.calls.Load() != 0 {
		t.Fatal("notice timer launched an agent")
	}
	if _, err = os.Stat(filepath.Join(root, "registry.json")); !os.IsNotExist(err) {
		t.Fatal("notice publication bootstrapped unrelated registry")
	}
}
