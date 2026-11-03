//go:build darwin || linux

package runtime_test

import (
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

func TestExecutionJournalPrivateReservationTransitionsAndProfileImmutability(t *testing.T) {
	_, descriptor, _ := ptyFixture(t)
	root := daemonRoot(t)
	store, err := infra.NewExecutionStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	profile := descriptor.Snapshot().Profile
	if err = store.RegisterProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if err = store.RegisterProfile(ctx, profile); !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("duplicate profile: %v", err)
	}
	if err = store.ReserveLaunch(ctx, descriptor.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err = store.ReserveLaunch(ctx, descriptor.Snapshot()); !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("reservation replay: %v", err)
	}
	if err = descriptor.Started(123); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveLaunch(ctx, descriptor.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err = descriptor.Exit(7); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveLaunch(ctx, descriptor.Snapshot()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.ListLaunches(ctx)
	if err != nil || len(snapshot) != 1 || snapshot[0] != descriptor.Snapshot() {
		t.Fatalf("journal roundtrip: %v", err)
	}
	snapshot[0].PID = 999
	again, err := store.ListLaunches(ctx)
	if err != nil || again[0].PID != 123 {
		t.Fatal("journal snapshot mutated")
	}
	file := filepath.Join(root, "execution.json")
	info, err := os.Lstat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("journal not private")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"FIXTURE_RESULT", "GORACE", "GIT_DIR", "TRACEARY_PTY_FIXTURE", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatal("journal contains environment or raw terminal data")
		}
	}
}
func TestExecutionJournalRejectsCorruptionAndAggregateBudgetBeforeMutation(t *testing.T) {
	_, descriptor, _ := ptyFixture(t)
	ctx := context.Background()
	t.Run("aggregate", func(t *testing.T) {
		root := daemonRoot(t)
		store, err := infra.NewExecutionStore(root, 3000)
		if err != nil {
			t.Fatal(err)
		}
		// A safe sparse registry file consumes the shared envelope budget, even
		// though this adapter never interprets or modifies registry contents.
		f, err := os.OpenFile(filepath.Join(root, "registry.json"), os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.Truncate(2990); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		if err = store.RegisterProfile(ctx, descriptor.Snapshot().Profile); !errors.Is(err, domain.ErrLimit) {
			t.Fatalf("aggregate overflow: %v", err)
		}
		if _, err = os.Stat(filepath.Join(root, "execution.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("overflow created metadata")
		}
	})
	for _, body := range []string{`{"version":99}`, `{"version":1,"revision":0,"profiles":null,"launches":[]}`, `{"version":1,"revision":0,"profiles":[],"launches":[],"Profiles":[]}`, `{"version":1,"revision":null,"profiles":[],"launches":[]}`} {
		root := daemonRoot(t)
		store, err := infra.NewExecutionStore(root, infra.MaxRegistryBytes)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "execution.json")
		if err = os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = store.ListLaunches(ctx); err == nil {
			t.Fatal("corrupt execution metadata accepted")
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != body {
			t.Fatal("corruption was repaired or overwritten")
		}
	}
}

func TestRegistryWritesShareExecutionMetadataBudget(t *testing.T) {
	root := daemonRoot(t)
	store, err := infra.NewRegistryStore(root, 3000)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(root, "execution.json"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(2990); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	p, err := domain.ProjectOf("/project", "/project/.git", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(context.Background(), func(r *domain.Registry) error { return r.AddProject(p) })
	if !errors.Is(err, domain.ErrLimit) {
		t.Fatalf("registry ignored other metadata: %v", err)
	}
	if _, err = os.Stat(filepath.Join(root, "registry.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("overflow mutated registry")
	}
}
func TestPTYRuntimeDurableReservationLaunchAndOwnedExit(t *testing.T) {
	launcher, descriptor, _ := ptyFixture(t)
	root := filepath.Dir(descriptor.Target().Worktree().Path())
	registry, _ := service(t, root)
	journal, err := infra.NewExecutionStore(filepath.Join(root, "metadata"), infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = journal.RegisterProfile(ctx, descriptor.Snapshot().Profile); err != nil {
		t.Fatal(err)
	}
	owner, err := app.NewRuntime(registry, journal, launcher, journal, descriptor.Identity().Epoch)
	if err != nil {
		t.Fatal(err)
	}
	req := app.LaunchRequest{Epoch: descriptor.Identity().Epoch, Worktree: descriptor.Target().Worktree().ID(), Profile: descriptor.Target().Profile(), RequestID: "owned-pty-launch"}
	launched, err := owner.Launch(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	exited, err := owner.AwaitExit(deadline, launched.Identity)
	if err != nil || exited.State != domain.RunExited || exited.ExitCode != 7 {
		t.Fatalf("owned native Wait: %v", err)
	}
	records, err := journal.ListLaunches(ctx)
	if err != nil || len(records) != 1 || records[0] != exited {
		t.Fatalf("durable owned exit: %v", err)
	}
	if owner.HasActive() {
		t.Fatal("exited child retained active capacity")
	}
}
