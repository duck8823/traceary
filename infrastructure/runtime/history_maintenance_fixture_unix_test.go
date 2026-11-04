//go:build darwin || linux

package runtime_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	"golang.org/x/xerrors"
)

// This gate owns the REAL exclusive lease. It pauses only after actual OS
// acquisition and forwards actual process-held ownership to prepared upgrade.
type gatedHistoryMaintenanceLease struct {
	admitted chan struct{}
	proceed  <-chan struct{}
}

func (g gatedHistoryMaintenanceLease) AcquireExclusive(ctx context.Context, path string) (func(), error) {
	release, err := (sqlite.StoreLeaseCoordinator{}).AcquireExclusive(ctx, path)
	if err != nil {
		return nil, xerrors.Errorf("acquire real fixture maintenance lease: %w", err)
	}
	close(g.admitted)
	select {
	case <-g.proceed:
		return release, nil
	case <-ctx.Done():
		release()
		return nil, xerrors.Errorf("release canceled fixture maintenance gate: %w", ctx.Err())
	}
}
func (g gatedHistoryMaintenanceLease) HoldsExclusive(path string) bool {
	return (sqlite.StoreLeaseCoordinator{}).HoldsExclusive(path)
}

// One actual Runtime owns one registered finite PTY. The fixture never probes
// or signals saved PIDs, invents native conversation binding or starts a real AI.
type historyMaintenancePTY struct {
	owner    *app.Runtime
	registry *app.Registry
	run      domain.RunSnapshot
	release  string
}

func startHistoryMaintenancePTY(ctx context.Context, t *testing.T, f historySQLiteFixture) *historyMaintenancePTY {
	t.Helper()
	metadataRoot := filepath.Join(filepath.Dir(f.path), "metadata")
	metadata, err := infra.OpenRegistryStore(metadataRoot, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := app.NewRegistry(metadata, infra.RegistryResolver{})
	if err != nil {
		t.Fatal(err)
	}
	journal, err := infra.OpenExecutionStore(metadataRoot, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(filepath.Dir(f.path), "maintenance-child.json")
	release := result + ".release"
	launcher, err := infra.NewPTYLauncher(registry, []string{"TRACEARY_PTY_FIXTURE=1", "FIXTURE_OUTPUT_MODE=maintenance", "FIXTURE_RESULT=" + result, "FIXTURE_RELEASE=" + release, "GORACE=atexit_sleep_ms=0"})
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	profile, err := launcher.InspectProfile(ctx, "codex-fixture", "codex", executable, "fixture 1")
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.RegisterProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	epoch := strings.Repeat("3", 32)
	owner, err := app.NewRuntime(registry, journal, launcher, journal, epoch)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		owner.DisableLaunches()
		_ = os.WriteFile(release, []byte("release finite owned fixture"), 0600)
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := owner.AwaitIdle(cleanup); e != nil {
			t.Errorf("owned PTY cleanup did not reconcile: %v", e)
		}
	})
	run, err := owner.Launch(ctx, app.LaunchRequest{Epoch: epoch, Worktree: f.request.Worktree, Profile: profile.ID(), RequestID: "history-real-maintenance"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, e := os.Stat(result + ".maintenance-ready"); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finite maintenance PTY fixture was not ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return &historyMaintenancePTY{owner, registry, run, release}
}

func (n *historyMaintenancePTY) assertLive(t *testing.T) {
	t.Helper()
	runs := n.owner.List()
	if len(runs) != 1 || runs[0].Identity != n.run.Identity || runs[0].State != domain.RunLive {
		t.Fatal("maintenance closed/replaced the actual owned PTY")
	}
}

func (n *historyMaintenancePTY) finish(ctx context.Context, t *testing.T) {
	t.Helper()
	if err := os.WriteFile(n.release, []byte("release after real maintenance"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := n.owner.AwaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	runs := n.owner.List()
	if len(runs) != 1 || runs[0].State != domain.RunExited || runs[0].ExitCode != 7 {
		t.Fatal("finite owned PTY did not survive real maintenance to owned exit")
	}
	sub, err := n.owner.Attach(app.OutputSelection{Identity: n.run.Identity, Worktree: n.run.Target.Worktree().ID(), Profile: n.run.Profile.ID()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	var captured bytes.Buffer
	ended := false
	for frames := 0; frames < 8; frames++ {
		frame, e := sub.Next(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if frame.Kind == "gap" {
			t.Fatal("maintenance lost finite PTY capture")
		}
		_, _ = captured.Write(frame.Data)
		if frame.Kind == "end" {
			ended = true
			break
		}
	}
	if !ended || !bytes.Contains(captured.Bytes(), []byte("BEFORE")) || !bytes.Contains(captured.Bytes(), []byte("AFTER")) {
		t.Fatal("actual owned PTY output did not survive across real store exchange")
	}
}

type historyMaintenanceScan struct {
	cancel          context.CancelFunc
	done            chan struct{}
	err             error
	successes, busy atomic.Int64
}

func startHistoryMaintenanceScan(parent context.Context, t *testing.T, f historySQLiteFixture) *historyMaintenanceScan {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	s := &historyMaintenanceScan{cancel: cancel, done: make(chan struct{})}
	ready := make(chan struct{})
	t.Cleanup(func() { s.stop(t) })
	go func() {
		defer close(s.done)
		announced := false
		for {
			_, err := f.history.Page(ctx, f.request)
			if err == nil {
				s.successes.Add(1)
				if !announced {
					close(ready)
					announced = true
				}
			} else if errors.Is(err, app.ErrRuntimeBusy) {
				s.busy.Add(1)
			} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				if ctx.Err() != nil {
					return
				}
				// A bounded individual read may expire without canceling this
				// independent periodic consumer. Keep requesting fresh pages;
				// never silently stop the contention workload on that timeout.
			} else if !errors.Is(err, domain.ErrChanged) {
				s.err = err
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	select {
	case <-ready:
	case <-s.done:
		t.Fatal("continuous reader stopped before maintenance", s.err)
	case <-parent.Done():
		t.Fatal("continuous reader not ready")
	}
	return s
}
func (s *historyMaintenanceScan) stop(t *testing.T) {
	t.Helper()
	s.cancel()
	select {
	case <-s.done:
		if s.err != nil {
			t.Errorf("continuous actual history read failed: %v", s.err)
		}
	case <-time.After(3 * time.Second):
		t.Error("history reader did not release/join")
	}
}
