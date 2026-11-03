//go:build darwin || linux

package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
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
		if len(os.Args) == 2 && os.Args[1] == "--fixture-hold-pty" {
			time.Sleep(800 * time.Millisecond)
			os.Exit(0)
		}

		if len(os.Args) == 2 && os.Args[1] == "--version" {
			if delay, err := time.ParseDuration(os.Getenv("FIXTURE_VERSION_DELAY")); err == nil && delay > 0 && delay < 2*time.Second {
				time.Sleep(delay)
			}
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
		if os.Getenv("FIXTURE_OUTPUT_MODE") == "descendant" {
			signal.Ignore(syscall.SIGHUP)
			executable, e := os.Executable()
			if e != nil {
				os.Exit(95)
			}
			child := exec.Command(executable, "--fixture-hold-pty")
			child.Env = os.Environ()
			child.Stdin = os.Stdin
			child.Stdout = os.Stdout
			child.Stderr = os.Stderr
			if child.Start() != nil {
				os.Exit(96)
			}
			_, _ = os.Stdout.Write([]byte("TAIL"))
			os.Exit(7)
		}
		if os.Getenv("FIXTURE_OUTPUT_MODE") == "unicode" {
			if _, e := term.MakeRaw(1); e != nil {
				os.Exit(93)
			}
			for i := 0; i < 300; i++ {
				if e := unix.IoctlSetWinsize(1, unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(24 + i%20), Col: uint16(80 + i%40)}); e != nil {
					os.Exit(94)
				}
			}
			_, _ = os.Stdout.Write([]byte("日本語🙂\x1b[2J\x1b[H\x1b]52;c;ZmFrZQ==\a\x1b]8;;https://example.invalid\aTAIL"))
			os.Exit(7)
		}
		// A missing continuous drain would deadlock the child on this >PTY capacity write.
		if _, err = os.Stdout.Write(bytes.Repeat([]byte("x"), 2*1024*1024)); err != nil {
			os.Exit(91)
		}
		_ = os.WriteFile(os.Getenv("FIXTURE_RESULT")+".output-ready", []byte("ready"), 0600)
		if release := os.Getenv("FIXTURE_RELEASE"); release != "" {
			deadline := time.Now().Add(15 * time.Second)
			for {
				if _, err := os.Stat(release); err == nil {
					break
				}
				if time.Now().After(deadline) {
					os.Exit(92)
				}
				time.Sleep(5 * time.Millisecond)
			}
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
	// Reconnect to the same exited owned child; buffered kernel tail must survive Wait.
	source, ok := child.(app.OutputChild)
	if !ok {
		t.Fatal("PTY child does not expose owned output")
	}
	selection := app.OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}
	sub, err := source.SubscribeOutput(selection, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	frame, err := sub.Next(context.Background())
	if err != nil || frame.Kind != "snapshot" || frame.Info.Head != 2*1024*1024 || frame.Info.Resize.Rows != 24 || frame.Info.Resize.Cols != 80 {
		t.Fatal("incorrect output snapshot or native exit tail")
	}
	var outputBytes int
	for {
		frame, err = sub.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if frame.Kind == "end" {
			break
		}
		if frame.Kind != "data" || !bytes.Equal(frame.Data, bytes.Repeat([]byte("x"), len(frame.Data))) {
			t.Fatal("unexpected output gap or changed bytes")
		}
		outputBytes += len(frame.Data)
	}
	if outputBytes != 2*1024*1024 {
		t.Fatal("native output lost before owned exit")
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

func TestPTYInvalidStaticProfileRefusedBeforeMissingPathInspection(t *testing.T) {
	root, _, _ := fixture(t)
	registry, _ := service(t, root)
	launcher, err := infra.NewPTYLauncher(registry, []string{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = launcher.InspectProfile(context.Background(), "../bad", "shell", filepath.Join(root, "nonexistent"), "")
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid profile unnecessarily inspected path: %v", err)
	}
}

func TestPTYRawUnicodeTUICaptureAndNativeResizeDoNotForgeOwnerEvidence(t *testing.T) {
	launcher, descriptor, _ := ptyFixture(t, "FIXTURE_OUTPUT_MODE=unicode")
	child, err := launcher.Start(context.Background(), descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if code, e := child.Wait(); e != nil || code != 7 {
		t.Fatal("synthetic raw fixture failed")
	}
	snapshot := descriptor.Snapshot()
	selection := app.OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}
	source, ok := child.(app.OutputChild)
	if !ok {
		t.Fatal("missing output port")
	}
	sub, err := source.SubscribeOutput(selection, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first, err := sub.Next(context.Background())
	if err != nil || first.Info.Resize.Sequence != 0 || first.Info.Resize.Rows != 24 || first.Info.Resize.Cols != 80 {
		t.Fatal("child geometry forged an owner acknowledgment")
	}
	var got []byte
	for {
		frame, e := sub.Next(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if frame.Kind == "end" {
			break
		}
		if frame.Kind != "data" {
			t.Fatal("unexpected raw byte gap")
		}
		got = append(got, frame.Data...)
	}
	want := []byte("日本語🙂\x1b[2J\x1b[H\x1b]52;c;ZmFrZQ==\a\x1b]8;;https://example.invalid\aTAIL")
	if !bytes.Equal(got, want) {
		t.Fatal("raw Unicode, TUI escapes or exit tail changed")
	}
}

func TestPTYDescendantHeldSlaveUsesNativeEOFOrBoundedUnknownTail(t *testing.T) {
	launcher, descriptor, _ := ptyFixture(t, "FIXTURE_OUTPUT_MODE=descendant")
	child, err := launcher.Start(context.Background(), descriptor)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	if code, e := child.Wait(); e != nil || code != 7 {
		t.Fatal("synthetic descendant fixture failed")
	}
	// The descendant deliberately retains the slave for 800ms; root Wait cannot wait for it.
	if time.Since(before) > 650*time.Millisecond {
		t.Fatal("descendant retained owned reconciliation")
	}
	snapshot := descriptor.Snapshot()
	selection := app.OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}
	source := child.(app.OutputChild)
	sub, err := source.SubscribeOutput(selection, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	snapshotFrame, _ := sub.Next(context.Background())
	var data []byte
	var unknown bool
	for {
		frame, e := sub.Next(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if frame.Kind == "end" {
			break
		}
		if frame.Kind == "data" {
			data = append(data, frame.Data...)
		}
		if frame.Gap != nil {
			unknown = frame.Gap.Reason == "tail_unknown" && frame.Gap.DeliveryUnknown && frame.Gap.From == frame.Gap.To
		}
	}
	if string(data) != "TAIL" || !unknown && snapshotFrame.Info.Reason != app.CaptureClosed {
		t.Fatalf("kernel tail lost or descendant cutoff hidden: bytes=%d unknown=%v", len(data), unknown)
	}
}
