//go:build darwin || linux

package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// TestMain is the explicitly selected fixture executable, not a real AI CLI.
func TestMain(m *testing.M) {
	if os.Getenv("TRACEARY_PTY_FIXTURE") == "1" {
		if len(os.Args) == 2 && os.Args[1] == "--version" {
			switch os.Getenv("FIXTURE_VERSION_MODE") {
			case "huge":
				_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 5000))
			case "slow":
				time.Sleep(3 * time.Second)
			default:
				_, _ = io.WriteString(os.Stdout, "fixture 1\n")
			}
			os.Exit(0)
		}
		tty, err := os.Open("/dev/tty")
		if err == nil {
			_ = tty.Close()
		}
		cwd, _ := os.Getwd()
		result := struct {
			TTY, Controlling, NewGroup                      bool
			CWD, Store, Run, Generation, Epoch, GitOverride string
		}{term.IsTerminal(0) && term.IsTerminal(1) && term.IsTerminal(2), err == nil, unix.Getpgrp() == os.Getpid(), cwd, os.Getenv("TRACEARY_DB_PATH"), os.Getenv("TRACEARY_RUNTIME_RUN_ID"), os.Getenv("TRACEARY_RUNTIME_GENERATION"), os.Getenv("TRACEARY_RUNTIME_EPOCH"), os.Getenv("GIT_DIR")}
		data, _ := json.Marshal(result)
		if os.WriteFile(os.Getenv("FIXTURE_RESULT"), data, 0600) != nil {
			os.Exit(90)
		}
		// A missing continuous drain would deadlock the child on this >PTY capacity write.
		if _, err = os.Stdout.Write(bytes.Repeat([]byte("x"), 2*1024*1024)); err != nil {
			os.Exit(91)
		}
		os.Exit(7)
	}
	os.Exit(m.Run())
}
func ptyFixture(t *testing.T, extra ...string) (*infra.PTYLauncher, domain.LaunchDescriptor, string) {
	t.Helper()
	root, repo, db := fixture(t)
	registry, _ := service(t, root)
	p, err := registry.RegisterProject(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	w, err := registry.RegisterWorktree(context.Background(), p.ID(), repo, db)
	if err != nil {
		t.Fatal(err)
	}
	target, err := registry.Resolve(context.Background(), w.ID(), "codex-fixture")
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(root, "child-result.json")
	env := append([]string{"TRACEARY_PTY_FIXTURE=1", "FIXTURE_RESULT=" + result, "GIT_DIR=/ignored/ambient", "TRACEARY_DB_PATH=/ignored/ambient", "GORACE=atexit_sleep_ms=0"}, extra...)
	launcher, err := infra.NewPTYLauncher(registry, env)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := launcher.InspectProfile(context.Background(), "codex-fixture", "codex", executable, "fixture 1")
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := domain.LaunchDescriptorOf(domain.LaunchIdentity{Run: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32), Epoch: strings.Repeat("3", 32)}, target, profile)
	if err != nil {
		t.Fatal(err)
	}
	return launcher, descriptor, result
}
func TestPTYDirectBinaryControllingTerminalTargetAndOwnedWait(t *testing.T) {
	launcher, descriptor, result := ptyFixture(t)
	child, err := launcher.Start(context.Background(), descriptor)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		code, err := child.Wait()
		if err == nil && code != 7 {
			err = errors.New("incorrect owned exit code")
		}
		done <- err
	}()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("PTY drain or owned Wait stalled")
	}
	data, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		TTY, Controlling, NewGroup                      bool
		CWD, Store, Run, Generation, Epoch, GitOverride string
	}
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	snapshot := descriptor.Snapshot()
	if !got.TTY || !got.Controlling || !got.NewGroup || got.CWD != snapshot.Target.Worktree().Path() || got.Store != snapshot.Target.Worktree().Store() || got.Run != snapshot.Identity.Run || got.Generation != snapshot.Identity.Generation || got.Epoch != snapshot.Identity.Epoch || got.GitOverride != "" {
		t.Fatal("incorrect native PTY/target/environment binding")
	}
	if child.PID() <= 0 {
		t.Fatal("missing diagnostic PID")
	}
	// Adapter caches its single exec.Cmd.Wait result; this is not a second OS wait.
	if code, err := child.Wait(); err != nil || code != 7 {
		t.Fatalf("owned cached wait: %v", err)
	}
}
func TestPTYChangedTargetAndExecutableWitnessRefuseBeforeSpawn(t *testing.T) {
	launcher, descriptor, result := ptyFixture(t)
	state := descriptor.Snapshot().Profile.Snapshot()
	state.ExecutableWitness = "changed"
	profile, err := domain.HostProfileFrom(state)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := domain.LaunchDescriptorOf(descriptor.Identity(), descriptor.Target(), profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = launcher.Start(context.Background(), changed); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("changed executable: %v", err)
	}
	store := descriptor.Target().Worktree().Store()
	if err = os.Rename(store, store+".old"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(store, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = launcher.Start(context.Background(), descriptor); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("changed store: %v", err)
	}
	if _, err = os.Stat(result); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refused launch still ran child")
	}
}
func TestPTYProfileProbeRefusesUnsafeModeUnknownHostVersionAndOversize(t *testing.T) {
	root, _, _ := fixture(t)
	path := filepath.Join(root, "unsafe-agent")
	marker := filepath.Join(root, "executed")
	if err := os.WriteFile(path, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0777); err != nil {
		t.Fatal(err)
	}
	registry, _ := service(t, root)
	launcher, err := infra.NewPTYLauncher(registry, []string{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = launcher.InspectProfile(context.Background(), "test", "codex", path, "fixture 1"); !errors.Is(err, domain.ErrUnsafe) {
		t.Fatalf("unsafe mode: %v", err)
	}
	if _, err = os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unsafe executable was probed")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"normal", "huge", "slow"} {
		t.Run(mode, func(t *testing.T) {
			launcher, err := infra.NewPTYLauncher(registry, []string{"TRACEARY_PTY_FIXTURE=1", "FIXTURE_VERSION_MODE=" + mode, "GORACE=atexit_sleep_ms=0"})
			if err != nil {
				t.Fatal(err)
			}
			expected := "fixture 1"
			if mode == "normal" {
				expected = "wrong version"
			}
			if _, err = launcher.InspectProfile(context.Background(), "fixture", "codex", executable, expected); !errors.Is(err, domain.ErrChanged) {
				t.Fatalf("unknown/oversize/slow version: %v", err)
			}
		})
	}
	if _, err = launcher.InspectProfile(context.Background(), "test", "shell", path, "fixture 1"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown host: %v", err)
	}
}

var _ app.ProcessLauncher = (*infra.PTYLauncher)(nil)
