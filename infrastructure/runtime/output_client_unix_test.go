//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"
)

func TestOutputClientRejectsConflictingResizeAtSameSequence(t *testing.T) {
	base := os.TempDir()
	if goruntime.GOOS == "darwin" {
		base = "/private/tmp"
	}
	root, err := os.MkdirTemp(base, "tws-")
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
	selection := outputWireSelection()
	info := app.OutputInfo{Selection: selection, Head: 1, Resize: app.ResizeEvidence{Sequence: 1, Rows: 30, Cols: 100}}
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
			var req runtimeRequest
			e = decodeRuntimeJSON(data, &req)
			return req, e
		}
		hello, e := read()
		if e != nil {
			done <- e
			return
		}
		if e = writeRuntimeFrame(conn, runtimeResponse{Version: RuntimeProtocolVersion, ID: hello.ID, Code: "ok", Role: "management"}); e != nil {
			done <- e
			return
		}
		attach, e := read()
		if e != nil {
			done <- e
			return
		}
		if e = writeRuntimeFrame(conn, runtimeResponse{Version: RuntimeProtocolVersion, ID: attach.ID, Code: "ok", Output: recordOutput(app.OutputFrame{Kind: "snapshot", Info: info})}); e != nil {
			done <- e
			return
		}
		conflict := info
		conflict.Resize.Rows++
		e = writeRuntimeFrame(conn, runtimeResponse{Version: RuntimeProtocolVersion, ID: attach.ID, Code: "ok", Output: recordOutput(app.OutputFrame{Kind: "data", Info: conflict, Start: 0, End: 1, Data: []byte("x")})})
		done <- e
	}()
	daemon, err := NewLocalDaemon(nil, DaemonOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := daemon.Attach(ctx, root, selection, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if _, err = stream.Next(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = stream.Next(ctx); !errors.Is(err, app.ErrRuntimeProtocol) {
		t.Fatalf("conflicting acknowledgment accepted: %v", err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
