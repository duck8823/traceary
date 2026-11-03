//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"io"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"
)

func TestShutdownSDKLostMalformedContradictoryAndQueuedAckNeverReplay(t *testing.T) {
	for _, mode := range []string{"lost", "malformed", "mismatched", "contradictory", "queued", "known_rejection", "delivered"} {
		t.Run(mode, func(t *testing.T) {
			base := os.TempDir()
			if goruntime.GOOS == "darwin" {
				base = "/private/tmp"
			}
			root, err := os.MkdirTemp(base, "tsa-")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.RemoveAll(root) }()
			root, err = filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "daemon.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			if err = os.Chmod(path, 0600); err != nil {
				t.Fatal(err)
			}
			binding := domain.ShutdownBindingState{Epoch: strings.Repeat("a", 32), PreviewID: strings.Repeat("b", 32), Revision: 7, RosterDigest: strings.Repeat("c", 64)}
			done := make(chan error, 1)
			go func() {
				conn, e := listener.AcceptUnix()
				if e != nil {
					done <- e
					return
				}
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				read := func() (runtimeRequest, error) {
					data, e := readRuntimeFrame(conn)
					if e != nil {
						return runtimeRequest{}, e
					}
					var r runtimeRequest
					e = decodeRuntimeJSON(data, &r)
					return r, e
				}
				hello, e := read()
				if e != nil {
					done <- e
					return
				}
				if e = writeRuntimeFrame(conn, runtimeResponse{Version: 1, ID: hello.ID, Code: "ok", Role: "management"}); e != nil {
					done <- e
					return
				}
				request, e := read()
				if e != nil {
					done <- e
					return
				}
				if request.Operation != "shutdown_confirm" || request.Lifecycle == nil || request.Lifecycle.Confirm == nil {
					done <- app.ErrRuntimeProtocol
					return
				}
				if mode == "lost" {
					done <- nil
					return
				}
				result := recordShutdown(app.ShutdownResult{Binding: binding, Outcome: domain.ShutdownDraining})
				reply := runtimeResponse{Version: 1, ID: request.ID, Code: "ok", Lifecycle: &lifecycleResponseRecord{Decision: &result}}
				switch mode {
				case "malformed":
					reply.Lifecycle = &lifecycleResponseRecord{}
				case "mismatched":
					reply.Lifecycle.Decision.Binding.PreviewID = strings.Repeat("d", 32)
				case "contradictory":
					reply.Code = "changed"
				case "queued":
					reply.Code = "queued"
				case "known_rejection":
					reply.Code = "changed"
					reply.Lifecycle = nil
				}
				if e = writeRuntimeFrame(conn, reply); e != nil {
					done <- e
					return
				}
				var extra [1]byte
				n, e := conn.Read(extra[:])
				if n != 0 || !errors.Is(e, io.EOF) {
					done <- app.ErrRuntimeProtocol
					return
				}
				done <- nil
			}()
			client, err := NewLocalDaemon(nil, DaemonOptions{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.ConfirmShutdown(context.Background(), root, app.ShutdownConfirmation{Binding: binding, WorkLossAcknowledged: true})
			switch mode {
			case "known_rejection":
				if !errors.Is(err, domain.ErrChanged) || result.Outcome != domain.ShutdownRejected {
					t.Fatal("known rejection not preserved", err)
				}
			case "delivered":
				if err != nil || result.Outcome != domain.ShutdownDraining {
					t.Fatal("valid confirmation not preserved", err)
				}
			default:
				if !errors.Is(err, app.ErrShutdownUnknown) || result.Outcome != domain.ShutdownUnknown || result.Binding != binding {
					t.Fatal("possible accepted shutdown falsely rejected/delivered", err)
				}
			}
			if e := <-done; e != nil {
				t.Fatal("fake peer or extra exchange failed", e)
			}
		})
	}
}

func TestLaunchSDKRejectsNewLifecycleAndQueuedOrContradictoryAck(t *testing.T) {
	project, err := domain.ProjectOf("/fixture", "/fixture/.git", "p")
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := domain.WorktreeOf(project.ID(), "/fixture", "/fixture/.git", "/fixture/store", "w")
	if err != nil {
		t.Fatal(err)
	}
	registry := domain.NewRegistry()
	if err = registry.AddProject(project); err != nil {
		t.Fatal(err)
	}
	if err = registry.AddWorktree(worktree); err != nil {
		t.Fatal(err)
	}
	target, err := registry.Target(worktree.ID(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "codex", Host: "codex", Executable: "/fixture/agent", ExecutableWitness: "w", Version: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	epoch := strings.Repeat("a", 32)
	descriptor, err := domain.LaunchDescriptorOf(domain.LaunchIdentity{Epoch: epoch, Run: strings.Repeat("b", 32), Generation: strings.Repeat("c", 32)}, target, profile)
	if err != nil {
		t.Fatal(err)
	}
	if err = descriptor.Started(123); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"shutdown_unknown", "queued", "changed"} {
		t.Run(code, func(t *testing.T) {
			root := receiptRoot(t)
			path := filepath.Join(root, "daemon.sock")
			listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = listener.Close() }()
			if e = os.Chmod(path, 0600); e != nil {
				t.Fatal(e)
			}
			done := make(chan error, 1)
			go func() {
				conn, e := listener.AcceptUnix()
				if e != nil {
					done <- e
					return
				}
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				read := func() (runtimeRequest, error) {
					data, e := readRuntimeFrame(conn)
					if e != nil {
						return runtimeRequest{}, e
					}
					var r runtimeRequest
					e = decodeRuntimeJSON(data, &r)
					return r, e
				}
				hello, e := read()
				if e != nil {
					done <- e
					return
				}
				if e = writeRuntimeFrame(conn, runtimeResponse{Version: 1, ID: hello.ID, Code: "ok", Role: "management"}); e != nil {
					done <- e
					return
				}
				request, e := read()
				if e != nil {
					done <- e
					return
				}
				record := recordLaunch(descriptor.Snapshot())
				done <- writeRuntimeFrame(conn, runtimeResponse{Version: 1, ID: request.ID, Code: code, Run: &record})
			}()
			client, e := NewLocalDaemon(nil, DaemonOptions{})
			if e != nil {
				t.Fatal(e)
			}
			_, e = client.Launch(context.Background(), root, app.LaunchRequest{Epoch: epoch, Worktree: worktree.ID(), Profile: "codex", RequestID: "one-launch"})
			if !errors.Is(e, app.ErrLaunchUnknown) {
				t.Fatal("possible committed launch misclassified by other-operation/contradictory ACK", e)
			}
			if e = <-done; e != nil {
				t.Fatal(e)
			}
		})
	}
}
