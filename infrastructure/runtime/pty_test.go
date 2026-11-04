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
		if len(os.Args) == 2 && os.Args[1] == "--fixture-scope-descendant" {
			role := os.Getenv("FIXTURE_DESC_ROLE")
			base := os.Getenv("FIXTURE_RESULT")
			_ = os.WriteFile(base+"."+role+".ready", []byte("ready"), 0600)
			time.Sleep(1500 * time.Millisecond)
			_ = os.WriteFile(base+"."+role+".survived", []byte("survived"), 0600)
			os.Exit(0)
		}
		if len(os.Args) == 2 && os.Args[1] == "--fixture-hold-pty" {
			time.Sleep(800 * time.Millisecond)
			os.Exit(0)
		}

		if len(os.Args) == 2 && os.Args[1] == "--version" {
			if path := os.Getenv("FIXTURE_VERSION_ENV_RESULT"); path != "" {
				_ = os.WriteFile(path, []byte(os.Getenv("TRACEARY_RUNTIME_DIR")), 0600)
			}
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
			TTY, Controlling, NewGroup                                   bool
			CWD, Store, Run, Generation, Epoch, GitOverride, RuntimeRoot string
		}{term.IsTerminal(0) && term.IsTerminal(1) && term.IsTerminal(2), err == nil, unix.Getpgrp() == os.Getpid(), cwd, os.Getenv("TRACEARY_DB_PATH"), os.Getenv("TRACEARY_RUNTIME_RUN_ID"), os.Getenv("TRACEARY_RUNTIME_GENERATION"), os.Getenv("TRACEARY_RUNTIME_EPOCH"), os.Getenv("GIT_DIR"), os.Getenv("TRACEARY_RUNTIME_DIR")}
		data, _ := json.Marshal(result)
		if os.WriteFile(os.Getenv("FIXTURE_RESULT"), data, 0600) != nil {
			os.Exit(90)
		}
		if os.Getenv("FIXTURE_OUTPUT_MODE") == "maintenance" {
			if _, e := io.WriteString(os.Stdout, "BEFORE\n"); e != nil {
				os.Exit(91)
			}
			if os.WriteFile(os.Getenv("FIXTURE_RESULT")+".maintenance-ready", []byte("ready"), 0600) != nil {
				os.Exit(90)
			}
			deadline := time.Now().Add(45 * time.Second)
			for {
				if _, e := os.Stat(os.Getenv("FIXTURE_RELEASE")); e == nil {
					break
				}
				if time.Now().After(deadline) {
					os.Exit(92)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if _, e := io.WriteString(os.Stdout, "AFTER\n"); e != nil {
				os.Exit(91)
			}
			os.Exit(7)
		}
		if os.Getenv("FIXTURE_OUTPUT_MODE") == "control_descendants" {
			executable, e := os.Executable()
			if e != nil {
				os.Exit(95)
			}
			for _, role := range []string{"group", "escaped"} {
				child := exec.Command(executable, "--fixture-scope-descendant")
				child.Env = append(os.Environ(), "FIXTURE_DESC_ROLE="+role)
				child.Stdin = os.Stdin
				child.Stdout = os.Stdout
				child.Stderr = os.Stderr
				if role == "escaped" {
					child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
				}
				if child.Start() != nil {
					os.Exit(96)
				}
				deadline := time.Now().Add(3 * time.Second)
				for {
					if _, e := os.Stat(os.Getenv("FIXTURE_RESULT") + "." + role + ".ready"); e == nil {
						break
					}
					if time.Now().After(deadline) {
						os.Exit(95)
					}
					time.Sleep(time.Millisecond)
				}
			}
			_ = os.WriteFile(os.Getenv("FIXTURE_RESULT")+".ready", []byte("ready"), 0600)
			time.Sleep(15 * time.Second)
			os.Exit(92)
		}
		if os.Getenv("FIXTURE_OUTPUT_MODE") == "control" {
			if _, e := term.MakeRaw(0); e != nil {
				os.Exit(97)
			}
			_ = os.WriteFile(os.Getenv("FIXTURE_RESULT")+".ready", []byte("ready"), 0600)
			input := make([]byte, 5)
			if _, e := io.ReadFull(os.Stdin, input); e != nil {
				os.Exit(98)
			}
			if os.WriteFile(os.Getenv("FIXTURE_RESULT")+".input", input, 0600) != nil {
				os.Exit(99)
			}
			marker := make([]byte, 1)
			if _, e := io.ReadFull(os.Stdin, marker); e != nil {
				os.Exit(98)
			}
			size, e := unix.IoctlGetWinsize(0, unix.TIOCGWINSZ)
			if e != nil {
				os.Exit(99)
			}
			geometry, _ := json.Marshal([]uint16{size.Row, size.Col})
			_ = os.WriteFile(os.Getenv("FIXTURE_RESULT")+".geometry", geometry, 0600)
			time.Sleep(15 * time.Second)
			os.Exit(92)
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
	launcher, descriptor, result := ptyFixture(t, "TRACEARY_RUNTIME_DIR=/untrusted/ambient")
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
		RuntimeRoot                                     string
	}
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	snapshot := descriptor.Snapshot()
	if !got.TTY || !got.Controlling || !got.NewGroup || got.CWD != snapshot.Target.Worktree().Path() || got.Store != snapshot.Target.Worktree().Store() || got.Run != snapshot.Identity.Run || got.Generation != snapshot.Identity.Generation || got.Epoch != snapshot.Identity.Epoch || got.GitOverride != "" {
		t.Fatal("incorrect native PTY/target/environment binding")
	}
	if got.RuntimeRoot != "" {
		t.Fatal("legacy launcher leaked ambient routing")
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

func TestPTYExplicitRawInputResizeAndOwnedGroupStop(t *testing.T) {
	launcher, descriptor, result := ptyFixture(t, "FIXTURE_OUTPUT_MODE=control")
	child, err := launcher.Start(context.Background(), descriptor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() { _, _ = child.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(17 * time.Second):
			t.Error("synthetic child failed to finish cleanup")
		}
	})
	controller, ok := child.(app.ControlledChild)
	if !ok {
		t.Fatal("owned PTY has no explicit control capability")
	}
	stop, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "stop", Kind: domain.ControlStop, WorkLossAcknowledged: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = controller.ApplyControl(context.Background(), stop, nil) })
	waitFile := func(path string) []byte {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for {
			data, e := os.ReadFile(path)
			if e == nil {
				return data
			}
			if time.Now().After(deadline) {
				t.Fatal("synthetic native fixture did not produce expected file")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	_ = waitFile(result + ".ready")
	raw := []byte{0xe6, 0x97, 0xa5, 0x1b, 0xff}
	input, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "input", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: len(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if err = controller.ValidateControl(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	effect, err := controller.ApplyControl(context.Background(), input, raw)
	if err != nil || effect.Outcome != domain.ControlDelivered || effect.WrittenBytes != len(raw) {
		t.Fatalf("native input: %v", err)
	}
	if !bytes.Equal(waitFile(result+".input"), raw) {
		t.Fatal("raw input altered or Enter appended")
	}
	resize, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "resize", Kind: domain.ControlResize, ResizeSequence: 1, Rows: 31, Cols: 101})
	if err != nil {
		t.Fatal(err)
	}
	if err = controller.ValidateControl(context.Background(), resize); err != nil {
		t.Fatal(err)
	}
	if effect, err = controller.ApplyControl(context.Background(), resize, nil); err != nil || effect.Outcome != domain.ControlDelivered {
		t.Fatalf("native resize: %v", err)
	}
	if err = controller.ValidateControl(context.Background(), resize); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("new control reused an applied resize sequence")
	}
	marker, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "marker", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 1})
	if _, err = controller.ApplyControl(context.Background(), marker, []byte("r")); err != nil {
		t.Fatal(err)
	}
	if string(waitFile(result+".geometry")) != "[31,101]" {
		t.Fatal("resize acknowledgment preceded actual native application")
	}
	if _, err = controller.ApplyControl(context.Background(), stop, nil); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := child.Wait(); done <- e }()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("explicit owned group stop did not reconcile")
	}
	if _, err = controller.ApplyControl(context.Background(), input, raw); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("exited child accepted late input")
	}
	if _, err = controller.ApplyControl(context.Background(), stop, nil); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("exited child accepted late group signal")
	}
}

func TestPTYExplicitStopDoesNotSweepEscapedDescendants(t *testing.T) {
	launcher, descriptor, result := ptyFixture(t, "FIXTURE_OUTPUT_MODE=control_descendants")
	child, err := launcher.Start(context.Background(), descriptor)
	if err != nil {
		t.Fatal(err)
	}
	controller := child.(app.ControlledChild)
	stop, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "stop-group", Kind: domain.ControlStop, WorkLossAcknowledged: true})
	t.Cleanup(func() { _, _ = controller.ApplyControl(context.Background(), stop, nil); _, _ = child.Wait() })
	waitFile := func(path string) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for {
			if _, e := os.Stat(path); e == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("synthetic descendant scope evidence missing")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFile(result + ".ready")
	if _, err = controller.ApplyControl(context.Background(), stop, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = child.Wait(); err != nil {
		t.Fatal(err)
	}
	waitFile(result + ".escaped.survived")
	if _, err = os.Stat(result + ".group.survived"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("in-group descendant survived explicit group stop")
	}
}

func TestPTYControlChangedExecutableWitnessRefusesBeforeEffect(t *testing.T) {
	launcher, descriptor, result := ptyFixture(t, "FIXTURE_OUTPUT_MODE=control")
	original, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copyBinary := func(path string) {
		t.Helper()
		source, e := os.Open(original)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = source.Close() }()
		target, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if e != nil {
			t.Fatal(e)
		}
		_, e = io.Copy(target, source)
		closeErr := target.Close()
		if e != nil || closeErr != nil {
			t.Fatal("synthetic executable copy failed")
		}
	}
	path := filepath.Join(filepath.Dir(result), "registered-agent")
	copyBinary(path)
	profile, err := launcher.InspectProfile(context.Background(), string(descriptor.Target().Profile()), "codex", path, "fixture 1")
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err = domain.LaunchDescriptorOf(descriptor.Identity(), descriptor.Target(), profile)
	if err != nil {
		t.Fatal(err)
	}
	child, err := launcher.Start(context.Background(), descriptor)
	if err != nil {
		t.Fatal(err)
	}
	controller := child.(app.ControlledChild)
	stop, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "cleanup", Kind: domain.ControlStop, WorkLossAcknowledged: true})
	moved := false
	t.Cleanup(func() {
		if moved {
			_ = os.Rename(path+".original", path)
		}
		_, _ = controller.ApplyControl(context.Background(), stop, nil)
		_, _ = child.Wait()
	})
	deadline := time.Now().Add(4 * time.Second)
	for {
		if _, e := os.Stat(result + ".ready"); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("synthetic child not ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err = os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	moved = true
	copyBinary(path)
	input, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "refused", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 1})
	if err = controller.ValidateControl(context.Background(), input); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("changed executable identity accepted")
	}
	if _, err = controller.ApplyControl(context.Background(), input, []byte("x")); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("changed executable reached terminal effect")
	}
	if _, err = os.Stat(result + ".input"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refused executable witness still wrote input")
	}
}
