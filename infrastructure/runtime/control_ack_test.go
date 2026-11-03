//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"
)

func TestControlSDKLostMalformedAckUnknownWithoutRetry(t *testing.T) {
	for _, mode := range []string{"lost", "malformed", "known_rejection", "contradictory_rejection", "queued_success"} {
		t.Run(mode, func(t *testing.T) {
			base := os.TempDir()
			if goruntime.GOOS == "darwin" {
				base = "/private/tmp"
			}
			root, err := os.MkdirTemp(base, "tca-")
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
			done := make(chan error, 1)
			go func() {
				conn, e := listener.AcceptUnix()
				if e != nil {
					done <- e
					return
				}
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				read := func() (runtimeRequest, error) {
					data, e := readRuntimeFrame(conn)
					if e != nil {
						return runtimeRequest{}, e
					}
					var request runtimeRequest
					e = decodeRuntimeJSON(data, &request)
					return request, e
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
				request, e := read()
				if e != nil {
					done <- e
					return
				}
				if request.Operation != "input" || request.Control == nil {
					done <- app.ErrRuntimeProtocol
					return
				}
				switch mode {
				case "lost":
				case "malformed":
					e = writeRuntimeFrame(conn, runtimeResponse{Version: RuntimeProtocolVersion, ID: request.ID, Code: "ok", Control: &controlResultRecord{}})
				case "contradictory_rejection", "queued_success":
					decoded, e2 := request.Control.request("input")
					if e2 != nil {
						done <- e2
						return
					}
					result := app.ControlResult{Selection: decoded.Selection, Receipt: domain.ControlSnapshot{Identity: decoded.Selection.Identity, Intent: decoded.Intent.Snapshot(), Outcome: domain.ControlDelivered, WrittenBytes: 1}}
					code := "changed"
					if mode == "queued_success" {
						code = "queued"
					}
					e = writeRuntimeFrame(conn, runtimeResponse{Version: RuntimeProtocolVersion, ID: request.ID, Code: code, Control: recordControlResult(result)})
				case "known_rejection":
					e = writeRuntimeFrame(conn, runtimeResponse{Version: RuntimeProtocolVersion, ID: request.ID, Code: "changed"})
				}
				done <- e
			}()
			daemon, err := NewLocalDaemon(nil, DaemonOptions{})
			if err != nil {
				t.Fatal(err)
			}
			intent, _ := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "original", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 1})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			result, err := daemon.Control(ctx, root, app.ControlRequest{Selection: outputWireSelection(), Intent: intent, Data: []byte("x")})
			if mode == "known_rejection" {
				if !errors.Is(err, domain.ErrChanged) || errors.Is(err, app.ErrControlUnknown) {
					t.Fatal("correlated clean rejection made uncertain")
				}
			} else if !errors.Is(err, app.ErrControlUnknown) || result.Receipt.Intent.RequestID != "original" || result.Receipt.Outcome != domain.ControlUnknown {
				t.Fatalf("post-send ACK loss/malformed reply falsely confirmed: %v", err)
			}
			if e := <-done; e != nil {
				t.Fatal(e)
			}
		})
	}
}
