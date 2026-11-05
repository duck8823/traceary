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

func TestDaemonEmptyUpdateHandoffAtomicallyRetiresBeforeCleanAndReleasesLease(t *testing.T) {
	ctx := context.Background()
	root := receiptRoot(t)
	store, err := NewExecutionStore(root, MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	oldEpoch := strings.Repeat("a", 32)
	if _, err = store.RecordEpochStart(ctx, oldEpoch); err != nil {
		t.Fatal(err)
	}
	target := domain.UpdateTarget{Version: "v2", Protocol: 2}
	if err = store.SaveUpdateDeferral(ctx, oldEpoch, domain.UpdateDeferralState{Target: target, Started: time.Now().UTC().Round(0).Add(-25 * time.Hour), Notified24h: true}); err != nil {
		t.Fatal(err)
	}
	ports := &updateNoLaunchPorts{}
	owners := make(chan *app.Runtime, 1)
	d, err := NewLocalDaemon(nil, DaemonOptions{RuntimeFactory: func(ctx context.Context, path, epoch string) (*app.Runtime, error) {
		journal, e := OpenExecutionStore(path, MaxRegistryBytes)
		if e != nil {
			return nil, e
		}
		owner, e := app.NewRuntime(ports, journal, ports, journal, epoch)
		if e != nil {
			return nil, fmt.Errorf("construct handoff fixture: %w", e)
		}
		if _, e = owner.InitializeLifecycle(ctx); e != nil {
			return nil, fmt.Errorf("initialize handoff fixture: %w", e)
		}
		owners <- owner
		return owner, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	epoch := serveContinuityFixture(t, d, root)
	<-owners
	attempt, err := d.AttemptUpdate(ctx, root, app.UpdateAttemptRequest{Epoch: epoch, Target: target})
	if err != nil {
		t.Fatal(err)
	}
	binding, found := attempt.Preview.Value()
	if !found || binding.TotalRuns != 0 {
		t.Fatal("exact empty handoff preview missing")
	}
	result, err := d.HandoffUpdate(ctx, root, app.UpdateHandoffRequest{Target: target, Binding: binding, DaemonExitAcknowledged: true})
	if err != nil || result.Outcome != domain.ShutdownDraining || result.ClosedTerminals != 0 {
		t.Fatal("real empty handoff failed", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for d.serving.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if d.serving.Load() {
		t.Fatal("known empty handoff did not join daemon ownership")
	}
	snapshot, err := store.ReadLifecycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, deferred := snapshot.UpdateDeferral.Value()
	if snapshot.Epoch != epoch || !snapshot.Clean || deferred || len(snapshot.Shutdowns) != 1 || snapshot.Shutdowns[0] != result || ports.calls.Load() != 0 {
		t.Fatal("clean/outcome/retirement/zero-agent contract violated")
	}
	next, err := NewLocalDaemon(nil, DaemonOptions{})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareDaemon(ctx, next, root)
	if err != nil {
		t.Fatal("handoff retained old private lease", err)
	}
	prepared.shutdown()
}

func TestDaemonKnownEmptyShutdownCompletesSDKReplyBeforeNormalTeardown(t *testing.T) {
	for range 8 {
		ctx := context.Background()
		root := receiptRoot(t)
		ports := &updateNoLaunchPorts{}
		d, err := NewLocalDaemon(nil, DaemonOptions{RuntimeFactory: func(ctx context.Context, path, epoch string) (*app.Runtime, error) {
			store, e := OpenExecutionStore(path, MaxRegistryBytes)
			if e != nil {
				return nil, e
			}
			owner, e := app.NewRuntime(ports, store, ports, store, epoch)
			if e != nil {
				return nil, fmt.Errorf("construct empty shutdown fixture: %w", e)
			}
			if _, e = owner.InitializeLifecycle(ctx); e != nil {
				return nil, fmt.Errorf("initialize empty shutdown fixture: %w", e)
			}
			return owner, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		epoch := serveContinuityFixture(t, d, root)
		page, err := d.PreviewShutdown(ctx, root, epoch)
		if err != nil || page.Binding.TotalRuns != 0 {
			t.Fatal("empty SDK preview unavailable", err)
		}
		result, err := d.ConfirmShutdown(ctx, root, app.ShutdownConfirmation{Binding: page.Binding, WorkLossAcknowledged: true})
		if err != nil || result.Outcome != domain.ShutdownDraining || result.ClosedTerminals != 0 || ports.calls.Load() != 0 {
			t.Fatal("healthy empty response lost during normal teardown", err)
		}
	}
}
