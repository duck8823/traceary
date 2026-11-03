//go:build darwin || linux

package runtime_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

func daemonRoot(t *testing.T) string {
	t.Helper()
	base := os.TempDir()
	if runtime.GOOS == "darwin" {
		base = "/private/tmp"
	}
	root, err := os.MkdirTemp(base, "tri-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}
func daemon(t *testing.T, receiver app.ObservationReceiver, options infra.DaemonOptions) *infra.LocalDaemon {
	t.Helper()
	d, err := infra.NewLocalDaemon(receiver, options)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func startDaemon(t *testing.T, d *infra.LocalDaemon, root string) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Serve(ctx, root); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("daemon did not stop")
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := d.Status(context.Background(), root)
		if err == nil {
			if status.State != "listening" || status.ProtocolVersion != 1 || len(status.Epoch) != 32 {
				t.Fatal("invalid readiness status")
			}
			return cancel, done
		}
		select {
		case err := <-done:
			t.Fatalf("daemon startup: %v", err)
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("daemon did not become ready")
	return cancel, done
}
func TestDaemonStatusUsesPrivateSocketsWithoutCoreStore(t *testing.T) {
	root := daemonRoot(t)
	d := daemon(t, nil, infra.DaemonOptions{})
	_, _ = startDaemon(t, d, root)
	for _, name := range []string{"daemon.sock", "observations.sock"} {
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
			t.Fatalf("socket mode: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "registry.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("transport initialized registry")
	}
	if err := d.Observe(context.Background(), root, app.ObservationMetadata{Host: "codex", HostVersion: "fixture", RunID: "r", Generation: "g", Epoch: "e", TargetID: "w", EventID: "event", NativeSessionID: "s", Kind: "turn_boundary"}); !errors.Is(err, app.ErrRuntimeUnsupported) {
		t.Fatalf("no receiver must not falsely accept: %v", err)
	}
}
func TestDaemonAbsentAndUnsafePathsFailWithoutCreation(t *testing.T) {
	root := daemonRoot(t)
	d := daemon(t, nil, infra.DaemonOptions{})
	missing := filepath.Join(root, "missing")
	if _, err := d.Status(context.Background(), missing); !errors.Is(err, app.ErrDaemonUnavailable) {
		t.Fatalf("missing daemon: %v", err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("status created runtime directory")
	}
	marker := filepath.Join(root, "observations.sock")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.Serve(context.Background(), root); err == nil {
		t.Fatal("overwrote existing path")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "preserve" {
		t.Fatal("foreign artifact changed", err)
	}
	if _, err = os.Lstat(filepath.Join(root, "daemon.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial startup left listener")
	}
	alias := root + "-alias"
	if err = os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(alias) }()
	if _, err = d.Status(context.Background(), alias); !errors.Is(err, app.ErrUnsafeRuntime) {
		t.Fatalf("symlink root: %v", err)
	}
}

func rawFrame(t *testing.T, conn net.Conn, body string) {
	t.Helper()
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	if _, err := conn.Write(append(prefix[:], []byte(body)...)); err != nil {
		t.Fatal(err)
	}
}
func rawCode(t *testing.T, conn net.Conn) string {
	t.Helper()
	var prefix [4]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		t.Fatal(err)
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n > 65536 {
		t.Fatal("unbounded response")
	}
	data := make([]byte, int(n))
	if _, err := io.ReadFull(conn, data); err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		t.Fatal(err)
	}
	return reply.Code
}
func rawEndpoint(t *testing.T, root, name, role string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("unix", filepath.Join(root, name), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	rawFrame(t, conn, `{"protocol_version":1,"request_id":"hello","operation":"hello","role":"`+role+`"}`)
	if code := rawCode(t, conn); code != "ok" {
		t.Fatalf("hello: %s", code)
	}
	return conn
}
func TestDaemonWireRejectsSchemaVersionAndRoleEscalation(t *testing.T) {
	root := daemonRoot(t)
	receiver := &countReceiver{}
	d := daemon(t, receiver, infra.DaemonOptions{})
	_, _ = startDaemon(t, d, root)
	tests := []struct{ name, body, want string }{
		{"unknown version", `{"protocol_version":2,"request_id":"request","operation":"status","extra":true}`, "unsupported"},
		{"duplicate key", `{"protocol_version":1,"protocol_version":1,"request_id":"request","operation":"status"}`, "invalid"},
		{"case alias", `{"protocol_version":1,"request_id":"request","Operation":"status"}`, "invalid"},
		{"null optional role", `{"protocol_version":1,"request_id":"request","operation":"status","role":null}`, "invalid"},
		{"unknown field", `{"protocol_version":1,"request_id":"request","operation":"status","shell":"echo forbidden"}`, "invalid"},
		{"role change", `{"protocol_version":1,"request_id":"request","operation":"status","role":"observations"}`, "invalid"},
		{"arbitrary exec", `{"protocol_version":1,"request_id":"request","operation":"exec"}`, "unsupported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conn := rawEndpoint(t, root, "daemon.sock", "management")
			rawFrame(t, conn, test.body)
			if got := rawCode(t, conn); got != test.want {
				t.Fatalf("want %s got %s", test.want, got)
			}
		})
	}
	conn := rawEndpoint(t, root, "observations.sock", "observations")
	rawFrame(t, conn, `{"protocol_version":1,"request_id":"status","operation":"status"}`)
	if got := rawCode(t, conn); got != "unsupported" {
		t.Fatal("observation endpoint allowed management")
	}
	if receiver.calls.Load() != 0 {
		t.Fatal("rejected frames invoked observation consumer")
	}
}

type countReceiver struct{ calls atomic.Int32 }

func (r *countReceiver) ConsumeObservation(context.Context, app.ObservationMetadata) error {
	r.calls.Add(1)
	return nil
}
func TestDaemonRejectsOversizeTruncatedAndSlowFrames(t *testing.T) {
	root := daemonRoot(t)
	d := daemon(t, nil, infra.DaemonOptions{IOTimeout: 100 * time.Millisecond})
	_, _ = startDaemon(t, d, root)
	for _, mode := range []string{"oversize", "truncated", "slow"} {
		t.Run(mode, func(t *testing.T) {
			conn := rawEndpoint(t, root, "daemon.sock", "management")
			var prefix [4]byte
			switch mode {
			case "oversize":
				binary.BigEndian.PutUint32(prefix[:], 65537)
				_, _ = conn.Write(prefix[:])
			case "truncated":
				binary.BigEndian.PutUint32(prefix[:], 20)
				_, _ = conn.Write(append(prefix[:], '{'))
				if unix, ok := conn.(*net.UnixConn); ok {
					_ = unix.CloseWrite()
				}
			case "slow":
				_, _ = conn.Write([]byte{0})
			}
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			var data [1]byte
			if _, err := conn.Read(data[:]); err == nil {
				t.Fatal("invalid/slow frame stayed admitted")
			} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatal("client deadline expired before server rejected the frame")
			}
		})
	}
}
func metadataFixture() app.ObservationMetadata {
	return app.ObservationMetadata{Host: "codex", HostVersion: "fixture", RunID: "run", Generation: "generation", Epoch: "epoch", TargetID: "target", EventID: "event", NativeSessionID: "session", Kind: "turn_boundary"}
}

type blockedReceiver struct{ started chan struct{} }

func (r *blockedReceiver) ConsumeObservation(ctx context.Context, _ app.ObservationMetadata) error {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil
}
func TestDaemonObservationCountAndByteBackpressure(t *testing.T) {
	for _, bytes := range []bool{false, true} {
		t.Run(map[bool]string{false: "frames", true: "bytes"}[bytes], func(t *testing.T) {
			root := daemonRoot(t)
			receiver := &blockedReceiver{started: make(chan struct{}, 1)}
			options := infra.DaemonOptions{ObservationFrames: 1}
			if bytes {
				options.ObservationBytes = 1
			}
			d := daemon(t, receiver, options)
			_, _ = startDaemon(t, d, root)
			ctx := context.Background()
			if bytes {
				if err := d.Observe(ctx, root, metadataFixture()); !errors.Is(err, app.ErrRuntimeBusy) {
					t.Fatalf("byte budget: %v", err)
				}
				return
			}
			if err := d.Observe(ctx, root, metadataFixture()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-receiver.started:
			case <-time.After(time.Second):
				t.Fatal("consumer did not start")
			}
			if err := d.Observe(ctx, root, metadataFixture()); err != nil {
				t.Fatal(err)
			}
			if err := d.Observe(ctx, root, metadataFixture()); !errors.Is(err, app.ErrRuntimeBusy) {
				t.Fatalf("frame budget: %v", err)
			}
		})
	}
}
func TestDaemonCleanupPreservesReplacementAndPreventsConcurrentOwner(t *testing.T) {
	root := daemonRoot(t)
	d := daemon(t, nil, infra.DaemonOptions{})
	cancel, done := startDaemon(t, d, root)
	other := daemon(t, nil, infra.DaemonOptions{})
	if err := other.Serve(context.Background(), root); !errors.Is(err, app.ErrRuntimeBusy) {
		t.Fatalf("concurrent owner: %v", err)
	}
	path := filepath.Join(root, "daemon.sock")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "replacement" {
		t.Fatal("cleanup removed another artifact", err)
	}
}
func TestDaemonManagementConnectionsAreBounded(t *testing.T) {
	root := daemonRoot(t)
	d := daemon(t, nil, infra.DaemonOptions{ManagementConnections: 1})
	_, _ = startDaemon(t, d, root)
	// Readiness closes its client before the server necessarily releases the
	// only management slot. Wait for that observable release, not a fixed sleep.
	deadline := time.Now().Add(3 * time.Second)
	var admitted net.Conn
	for admitted == nil {
		conn, err := net.DialTimeout("unix", filepath.Join(root, "daemon.sock"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		rawFrame(t, conn, `{"protocol_version":1,"request_id":"hello","operation":"hello","role":"management"}`)
		code := rawCode(t, conn)
		if code == "ok" {
			admitted = conn
			break
		}
		_ = conn.Close()
		if code != "busy" || time.Now().After(deadline) {
			t.Fatalf("readiness slot did not release: %s", code)
		}
	}
	defer func() { _ = admitted.Close() }()
	conn, err := net.DialTimeout("unix", filepath.Join(root, "daemon.sock"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if got := rawCode(t, conn); got != "busy" {
		t.Fatalf("capacity: %s", got)
	}
}
func TestDaemonOptionsOnlyTightenAndPathLengthRejectsBeforeCreation(t *testing.T) {
	for _, option := range []infra.DaemonOptions{{ManagementConnections: 33}, {ObservationConnections: 33}, {ObservationFrames: 257}, {ObservationBytes: 8*1024*1024 + 1}, {IOTimeout: 2 * time.Second}} {
		if _, err := infra.NewLocalDaemon(nil, option); err == nil {
			t.Fatal("expanded fixed bounds")
		}
	}
	root := daemonRoot(t)
	long := filepath.Join(root, strings.Repeat("x", 110))
	d := daemon(t, nil, infra.DaemonOptions{})
	if err := d.Serve(context.Background(), long); !errors.Is(err, app.ErrUnsafeRuntime) {
		t.Fatalf("long path: %v", err)
	}
	if _, err := os.Stat(long); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid path caused mutation")
	}
}
func TestDaemonClientBudgetIncludesHandshakeAndDoesNotSpawn(t *testing.T) {
	root := daemonRoot(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(root, "daemon.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	defer func() { _ = listener.Close() }()
	if err = os.Chmod(filepath.Join(root, "daemon.sock"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.AcceptUnix()
		if err == nil {
			defer func() { _ = conn.Close() }()
			_, _ = io.Copy(io.Discard, conn)
		}
	}()
	d := daemon(t, nil, infra.DaemonOptions{})
	start := time.Now()
	_, err = d.Status(context.Background(), root)
	if err == nil {
		t.Fatal("unresponsive peer reported readiness")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("optional call ignored its budget: %s", elapsed)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed-out call retained connection")
	}
}

func TestDaemonLeaseReplacementInvalidatesExistingEndpoint(t *testing.T) {
	root := daemonRoot(t)
	d := daemon(t, nil, infra.DaemonOptions{})
	_, _ = startDaemon(t, d, root)
	lease := filepath.Join(root, "daemon.lock")
	if err := os.Rename(lease, lease+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lease, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Status(context.Background(), root); err == nil {
		t.Fatal("reported readiness after losing lifetime lease identity")
	}
}
func TestDaemonQueueLossRemainsDiagnosable(t *testing.T) {
	root := daemonRoot(t)
	d := daemon(t, &countReceiver{}, infra.DaemonOptions{ObservationBytes: 1})
	_, _ = startDaemon(t, d, root)
	if err := d.Observe(context.Background(), root, metadataFixture()); !errors.Is(err, app.ErrRuntimeBusy) {
		t.Fatal(err)
	}
	state, err := d.Status(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if state.ObservationDelivery != "degraded" {
		t.Fatal("queue loss was hidden by readiness status")
	}
}
func TestDaemonExactMaximumFrameAndDuplicateRequest(t *testing.T) {
	root := daemonRoot(t)
	d := daemon(t, nil, infra.DaemonOptions{})
	_, _ = startDaemon(t, d, root)
	conn, err := net.DialTimeout("unix", filepath.Join(root, "daemon.sock"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	hello := `{"protocol_version":1,"request_id":"hello","operation":"hello","role":"management"}`
	rawFrame(t, conn, hello+strings.Repeat(" ", 65536-len(hello)))
	if code := rawCode(t, conn); code != "ok" {
		t.Fatal("legal maximum frame rejected")
	}
	rawFrame(t, conn, `{"protocol_version":1,"request_id":"hello","operation":"status"}`)
	if code := rawCode(t, conn); code != "invalid" {
		t.Fatal("duplicate request was replayed")
	}
}

func TestDaemonCleanupPreservesReplacementSocket(t *testing.T) {
	root := daemonRoot(t)
	d := daemon(t, nil, infra.DaemonOptions{})
	cancel, done := startDaemon(t, d, root)
	path := filepath.Join(root, "daemon.sock")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	replacement.SetUnlinkOnClose(false)
	defer func() { _ = replacement.Close() }()
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("cleanup unlinked another socket", err)
	}
}
func TestDaemonCleanupDoesNotTouchReplacementRoot(t *testing.T) {
	root := daemonRoot(t)
	d := daemon(t, nil, infra.DaemonOptions{})
	cancel, done := startDaemon(t, d, root)
	moved := root + "-old"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(moved) }()
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "daemon.sock")
	if err := os.WriteFile(marker, []byte("new root"), 0600); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "new root" {
		t.Fatal("cleanup crossed into replacement root", err)
	}
}
