//go:build darwin || linux

package runtime_test

import (
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
	"golang.org/x/xerrors"
)

type recoveryChainRegistry struct{ target domain.Target }

func (r recoveryChainRegistry) Resolve(context.Context, domain.WorktreeID, string) (domain.Target, error) {
	return r.target, nil
}
func (r recoveryChainRegistry) Revalidate(context.Context, domain.Target, string) error { return nil }

// Predictable owned-Wait/native ports test Runtime + real atomic persistence.
// No native CLI, process signaling or host qualification is performed here.
type recoveryChainChild struct {
	binding domain.RecoveryBinding
	exited  chan struct{}
	pid     int
	late    bool
}

func (c *recoveryChainChild) PID() int           { return c.pid }
func (c *recoveryChainChild) Wait() (int, error) { <-c.exited; return 7, nil }
func (c *recoveryChainChild) RecoveryBinding(context.Context) (domain.RecoveryBinding, error) {
	if c.late {
		select {
		case <-c.exited:
		default:
			return domain.RecoveryBinding{}, domain.ErrUnsupported
		}
	}
	return c.binding, nil
}

type recoveryChainFactory struct {
	calls    atomic.Int32
	children [2]*recoveryChainChild
}

func (*recoveryChainFactory) Start(context.Context, domain.LaunchDescriptor) (app.OwnedChild, error) {
	return nil, domain.ErrUnsupported
}
func (*recoveryChainFactory) RevalidateRecovery(context.Context, domain.RecoveryBinding) error {
	return nil
}
func (f *recoveryChainFactory) Resume(_ context.Context, d domain.LaunchDescriptor, binding domain.RecoveryBinding) (app.OwnedChild, error) {
	index := f.calls.Add(1) - 1
	if index >= int32(len(f.children)) {
		return nil, domain.ErrChanged
	}
	c := f.children[index]
	b := binding.Snapshot()
	b.Identity = d.Identity()
	b.PID = c.pid
	value, err := domain.RecoveryBindingOf(b)
	if err != nil {
		return nil, xerrors.Errorf("construct synthetic recovery chain: %w", err)
	}
	c.binding = value
	return c, nil
}

func TestManagedRecoveryUnknownExitedSuccessorStartsOnlyNewExplicitRequest(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "same runtime", true: "new daemon epoch"}[restart], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			store, err := infra.OpenExecutionStore(root, infra.MaxRegistryBytes)
			if err != nil {
				t.Fatal(err)
			}
			project, err := domain.ProjectOf("/project", "/project/.git", "project-witness")
			if err != nil {
				t.Fatal(err)
			}
			worktree, err := domain.WorktreeOf(project.ID(), "/project", "/project/.git", "/store.db", "worktree-witness")
			if err != nil {
				t.Fatal(err)
			}
			profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "fixture", Host: "claude", Executable: "/agent", ExecutableWitness: "fixture-witness", Version: "synthetic 1"})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.RegisterProfile(ctx, profile); err != nil {
				t.Fatal(err)
			}
			target, err := domain.TargetOf(worktree, string(profile.ID()))
			if err != nil {
				t.Fatal(err)
			}
			identity := domain.LaunchIdentity{Run: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32), Epoch: strings.Repeat("3", 32)}
			original, err := domain.LaunchDescriptorOf(identity, target, profile)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.ReserveLaunch(ctx, original.Snapshot()); err != nil {
				t.Fatal(err)
			}
			if err := original.Started(321); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveLaunch(ctx, original.Snapshot()); err != nil {
				t.Fatal(err)
			}
			if err := original.Exit(7); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveLaunch(ctx, original.Snapshot()); err != nil {
				t.Fatal(err)
			}
			binding, err := domain.RecoveryBindingOf(domain.RecoveryBindingState{Identity: identity, Target: target, Profile: profile, PID: 321, NativeSessionID: "synthetic-native", NativeHistoryPath: "/native/history.jsonl", NativeNamespaceWitness: strings.Repeat("a", 64), Model: "synthetic-model", Provider: "synthetic-provider"})
			if err != nil {
				t.Fatal(err)
			}
			record, err := domain.NewRecoveryRecord(binding)
			if err != nil {
				t.Fatal(err)
			}
			if err := record.ObserveOwnedExit(identity, 7); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveRecovery(ctx, record.Snapshot()); err != nil {
				t.Fatal(err)
			}
			factory := &recoveryChainFactory{children: [2]*recoveryChainChild{{pid: 999, exited: make(chan struct{}), late: true}, {pid: 1001, exited: make(chan struct{})}}}
			owner, err := app.NewRuntime(recoveryChainRegistry{target: target}, store, factory, store, identity.Epoch)
			if err != nil {
				t.Fatal(err)
			}
			request := app.ResumeRequest{Epoch: identity.Epoch, Selection: app.OutputSelection{Identity: identity, Worktree: worktree.ID(), Profile: profile.ID()}, RequestID: "original-unknown", WorkLossAcknowledged: true}
			first, err := owner.Resume(ctx, request)
			if !errors.Is(err, app.ErrLaunchUnknown) || first.Phase != domain.ResumeUnknown || !first.Owned {
				t.Fatalf("actual owned unknown lost: %+v %v", first, err)
			}
			close(factory.children[0].exited)
			if err := owner.AwaitIdle(ctx); err != nil {
				t.Fatal(err)
			}
			receipts, err := store.ListRecovery(ctx)
			if err != nil || len(receipts) != 2 || !receipts[1].ExitObserved {
				t.Fatalf("qualified successor exit missing: %+v %v", receipts, err)
			}
			epoch := identity.Epoch
			if restart {
				epoch = strings.Repeat("c", 32)
				store, err = infra.OpenExecutionStore(root, infra.MaxRegistryBytes)
				if err != nil {
					t.Fatal(err)
				}
				owner, err = app.NewRuntime(recoveryChainRegistry{target: target}, store, factory, store, epoch)
				if err != nil {
					t.Fatal(err)
				}
			}
			later := app.ResumeRequest{Epoch: epoch, Selection: first.Selection, RequestID: "new-explicit-successor", WorkLossAcknowledged: true}
			second, err := owner.Resume(ctx, later)
			if err != nil || second.Phase != domain.ResumeConfirmed || !second.Owned {
				t.Fatalf("positive owned successor remained locked: %+v %v", second, err)
			}
			if second.Selection.Identity == first.Selection.Identity || second.Selection.Identity.Epoch != epoch {
				t.Fatal("new operation reused generation/epoch")
			}
			request.Epoch = epoch
			old, err := owner.Resume(ctx, request)
			if !errors.Is(err, app.ErrLaunchUnknown) || old.Phase != domain.ResumeUnknown || old.Selection != first.Selection {
				t.Fatalf("old unknown request rearmed/relabeled: %+v %v", old, err)
			}
			if factory.calls.Load() != 2 {
				t.Fatal("old request or restart created another native owner")
			}
			close(factory.children[1].exited)
			if err := owner.AwaitIdle(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
