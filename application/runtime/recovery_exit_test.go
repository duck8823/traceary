package runtime_test

import (
	"context"
	"errors"
	"golang.org/x/xerrors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// This synthetic owned-child adapter validates receipt production, not support
// for a real native CLI/version. Only its actual Wait may supply process exit.
type recoveryExitChild struct {
	*fakeChild
	binding            domain.RecoveryBinding
	sourceErr, waitErr error
	code               int
	sourceCalls        atomic.Int32
}

func (c *recoveryExitChild) Wait() (int, error) { <-c.exited; return c.code, c.waitErr }
func (c *recoveryExitChild) RecoveryBinding(context.Context) (domain.RecoveryBinding, error) {
	c.sourceCalls.Add(1)
	return c.binding, c.sourceErr
}

type recoveryExitFactory struct {
	child    *recoveryExitChild
	mismatch string
}

func (f recoveryExitFactory) Start(_ context.Context, d domain.LaunchDescriptor) (app.OwnedChild, error) {
	s := d.Snapshot()
	b := domain.RecoveryBindingState{Identity: s.Identity, Target: s.Target, Profile: s.Profile, PID: f.child.PID(), NativeSessionID: "synthetic-native", NativeHistoryPath: "/native/synthetic.jsonl", NativeNamespaceWitness: strings.Repeat("a", 64), Model: "synthetic-model", Provider: "synthetic-provider"}
	if f.mismatch == "identity" {
		b.Identity.Run = strings.Repeat("9", 32)
	}
	if f.mismatch == "pid" {
		b.PID++
	}
	binding, err := domain.RecoveryBindingOf(b)
	if err != nil {
		return nil, xerrors.Errorf("construct synthetic owned binding: %w", err)
	}
	f.child.binding = binding
	return f.child, nil
}

type recoveryExitJournal struct {
	*launchJournal
	mu                     sync.Mutex
	records                []domain.RecoveryRecordState
	failExit, failRecovery bool
	failLive               bool
}

func (j *recoveryExitJournal) SaveLaunch(ctx context.Context, s domain.RunSnapshot) error {
	if s.State == domain.RunLive && j.failLive {
		return domain.ErrUnsafe
	}
	if s.State == domain.RunExited && j.failExit {
		return domain.ErrUnsafe
	}
	return j.launchJournal.SaveLaunch(ctx, s)
}
func (j *recoveryExitJournal) SaveRecovery(_ context.Context, s domain.RecoveryRecordState) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.failRecovery {
		return domain.ErrUnsafe
	}
	j.records = append(j.records, s)
	return nil
}
func TestRuntimeRecoveryReceiptRequiresActualExactOwnedWaitAndDurableExit(t *testing.T) {
	for _, kind := range []string{"normal exit", "signal exit", "publication uncertainty", "source unavailable", "identity", "pid", "unknown wait", "exit write failure", "recovery write failure"} {
		t.Run(kind, func(t *testing.T) {
			_, request, base, registry := runtimeFixture(t, false)
			profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex-fixture", Host: "codex", Executable: "/owned/agent", ExecutableWitness: "fixture", Version: "fixture 1"})
			if err != nil {
				t.Fatal(err)
			}
			child := &recoveryExitChild{fakeChild: base.child, code: 7}
			journal := &recoveryExitJournal{launchJournal: base.journal}
			switch kind {
			case "signal exit":
				child.code = -1
			case "publication uncertainty":
				journal.failLive = true
			case "source unavailable":
				child.sourceErr = domain.ErrUnsupported
			case "unknown wait":
				child.waitErr = app.ErrLaunchUnknown
			case "exit write failure":
				journal.failExit = true
			case "recovery write failure":
				journal.failRecovery = true
			}
			owner, err := app.NewRuntime(registry, launchProfiles{profile}, recoveryExitFactory{child: child, mismatch: kind}, journal, request.Epoch)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			run, err := owner.Launch(ctx, request)
			if kind == "publication uncertainty" {
				if !errors.Is(err, app.ErrLaunchUnknown) {
					t.Fatalf("lost publication uncertainty: %v", err)
				}
			} else if err != nil {
				t.Fatalf("optional recovery changed normal launch: %v", err)
			}
			journal.mu.Lock()
			count := len(journal.records)
			journal.mu.Unlock()
			if count != 0 || child.sourceCalls.Load() != 0 {
				t.Fatal("receipt created before actual child Wait")
			}
			child.finish()
			exited, err := owner.AwaitExit(ctx, run.Identity)
			if kind == "unknown wait" {
				if !errors.Is(err, app.ErrLaunchUnknown) {
					t.Fatalf("unknown wait qualified: %v", err)
				}
			} else if err != nil {
				t.Fatalf("optional recovery changed normal exit: %v", err)
			}
			journal.mu.Lock()
			defer journal.mu.Unlock()
			want := kind == "normal exit" || kind == "signal exit" || kind == "publication uncertainty"
			if (kind == "unknown wait" || kind == "exit write failure") && child.sourceCalls.Load() != 0 {
				t.Fatal("native source read without durable owned exit")
			}
			if !want {
				if len(journal.records) != 0 {
					t.Fatal("invalid source/exit became a positive receipt")
				}
				return
			}
			if len(journal.records) != 1 {
				t.Fatalf("positive owned exit missing: %d", len(journal.records))
			}
			receipt := journal.records[0]
			binding := receipt.Binding.Snapshot()
			if !receipt.ExitObserved || receipt.ExitCode != child.code || binding.Identity != exited.Identity || binding.Target != exited.Target || binding.Profile != exited.Profile || binding.PID != exited.PID {
				t.Fatalf("receipt not bound to actual Wait: %+v", receipt)
			}
		})
	}
}
