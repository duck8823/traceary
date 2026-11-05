//go:build darwin || linux

package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"sync/atomic"
	"testing"
)

// This explicit synthetic owner exercises real IPC pagination, not native launch,
// signal ownership, durability, or real-host qualification.
type pagingTarget struct{ target domain.Target }

func (p pagingTarget) Resolve(context.Context, domain.WorktreeID, string) (domain.Target, error) {
	return p.target, nil
}
func (p pagingTarget) Revalidate(context.Context, domain.Target, string) error { return nil }

type pagingProfiles struct{ profile domain.HostProfile }

func (p pagingProfiles) LookupProfile(context.Context, domain.ProfileID) (domain.HostProfile, error) {
	return p.profile, nil
}

type pagingChild struct{ done <-chan struct{} }

func (p pagingChild) PID() int           { return 123 }
func (p pagingChild) Wait() (int, error) { <-p.done; return 0, nil }

type pagingLauncher struct {
	done  <-chan struct{}
	calls atomic.Int32
}

func (p *pagingLauncher) Start(context.Context, domain.LaunchDescriptor) (app.OwnedChild, error) {
	p.calls.Add(1)
	return pagingChild{p.done}, nil
}

type pagingJournal struct{}

func (pagingJournal) ReserveLaunch(context.Context, domain.RunSnapshot) error { return nil }
func (pagingJournal) SaveLaunch(context.Context, domain.RunSnapshot) error    { return nil }
func TestLifecycleSDKPagesFullRosterWithoutReplacingPlanAndRejectsStaleOrForeignRole(t *testing.T) {
	_, descriptor, _ := ptyFixture(t)
	ctx := context.Background()
	root := daemonRoot(t)
	released := make(chan struct{})
	defer close(released)
	launcher := &pagingLauncher{done: released}
	factory := func(_ context.Context, _ string, epoch string) (*app.Runtime, error) {
		return app.NewRuntime(pagingTarget{descriptor.Target()}, pagingProfiles{descriptor.Snapshot().Profile}, launcher, pagingJournal{}, epoch)
	}
	d := daemon(t, nil, infra.DaemonOptions{RuntimeFactory: factory})
	_, _ = startDaemon(t, d, root)
	status, err := d.Status(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	launch := app.LaunchRequest{Epoch: status.Epoch, Worktree: descriptor.Target().Worktree().ID(), Profile: descriptor.Snapshot().Profile.ID()}
	for i := range 3 {
		launch.RequestID = fmt.Sprintf("page-%d", i)
		if _, err = d.Launch(ctx, root, launch); err != nil {
			t.Fatal(err)
		}
	}
	first, err := d.PreviewShutdown(ctx, root, status.Epoch)
	if err != nil || len(first.Runs) != 2 || first.Next == "" {
		t.Fatal("initial page incorrect", err)
	}
	launch.RequestID = "mutates-preview-revision"
	if _, err = d.Launch(ctx, root, launch); err != nil {
		t.Fatal(err)
	}
	if _, err = d.ShutdownPage(ctx, root, app.ShutdownPageRequest{Binding: first.Binding, Cursor: first.Next, Offset: 2}); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("stale page silently refreshed plan", err)
	}
	if err = d.CancelShutdown(ctx, root, first.Binding); err != nil {
		t.Fatal(err)
	}
	for i := 4; i < domain.MaxActiveRuns; i++ {
		launch.RequestID = fmt.Sprintf("page-%d", i)
		if _, err = d.Launch(ctx, root, launch); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := app.ReadShutdownPlan(ctx, d, root, status.Epoch)
	if err != nil || len(plan.Runs) != domain.MaxActiveRuns || plan.Binding.TotalRuns != domain.MaxActiveRuns {
		t.Fatal("full256-run risk roster not assembled", err)
	}
	conn := rawEndpoint(t, root, "observations.sock", "observations")
	body, err := json.Marshal(map[string]any{"protocol_version": 1, "request_id": "no-lifecycle-role-escalation", "operation": "shutdown_preview", "lifecycle": map[string]any{"preview": map[string]string{"epoch": status.Epoch}}})
	if err != nil {
		t.Fatal(err)
	}
	rawFrame(t, conn, string(body))
	if code := rawCode(t, conn); code != "unsupported" {
		t.Fatal("observation role accepted lifecycle mutation", code)
	}
	if launcher.calls.Load() != domain.MaxActiveRuns {
		t.Fatal("pagination/observation caused launch effects")
	}
	if err = d.CancelShutdown(ctx, root, plan.Binding); err != nil {
		t.Fatal("complete plan replaced by page reads", err)
	}
}
