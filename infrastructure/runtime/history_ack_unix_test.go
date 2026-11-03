//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

func historyAckPeer(t *testing.T, endpoint string, reply func(runtimeRequest) runtimeResponse) (string, <-chan error) {
	t.Helper()
	base := os.TempDir()
	if goruntime.GOOS == "darwin" {
		base = "/private/tmp"
	}
	root, err := os.MkdirTemp(base, "tha-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(root, endpoint), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(root, endpoint), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close(); _ = os.RemoveAll(root) })
	done := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
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
		if e = writeRuntimeFrame(conn, runtimeResponse{Version: 1, ID: hello.ID, Code: "ok", Role: hello.Role}); e != nil {
			done <- e
			return
		}
		r, e := read()
		if e != nil {
			done <- e
			return
		}
		response := reply(r)
		if response.ID != "lost" {
			e = writeRuntimeFrame(conn, response)
		}
		done <- e
	}()
	return root, done
}
func awaitHistoryAck(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owned fake peer did not finish")
	}
}

func TestHistorySDKRejectsLostMixedWrongAndContradictoryReplies(t *testing.T) {
	for _, mode := range []string{"ok", "lost", "mixed", "wrong-id", "wrong-epoch", "wrong-witness", "missing-more", "queued", "error-data", "known-rejection"} {
		t.Run(mode, func(t *testing.T) {
			req, result := historyProtocolFixture(t)
			root, done := historyAckPeer(t, "daemon.sock", func(r runtimeRequest) runtimeResponse {
				reply := runtimeResponse{Version: 1, ID: r.ID, Code: "ok", History: recordHistoryResult(req, result)}
				switch mode {
				case "lost":
					reply.ID = "lost"
				case "mixed":
					h := recordHistoryHint(result.Hint)
					reply.HistoryHint = &h
				case "wrong-id":
					reply.ID = "different"
				case "wrong-epoch":
					reply.History.Request.Epoch = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
				case "wrong-witness":
					reply.History.Request.Selection.Witness = "other-source"
				case "missing-more":
					reply.History.Page.More = nil
				case "queued":
					reply.Code = "queued"
				case "error-data":
					reply.Code = "changed"
				case "known-rejection":
					reply.Code = "changed"
					reply.History = nil
				}
				return reply
			})
			d, err := NewLocalDaemon(nil, DaemonOptions{})
			if err != nil {
				t.Fatal(err)
			}
			got, err := d.HistoryPage(context.Background(), root, req)
			if mode == "ok" {
				if err != nil || got.Page.Next() != result.Page.Next() {
					t.Fatal(err)
				}
			} else if mode == "known-rejection" {
				if !errors.Is(err, domain.ErrChanged) {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("malformed history reply accepted")
			}
			awaitHistoryAck(t, done)
		})
	}
}
func TestOldLaunchSDKRejectsHistoryArmInsteadOfKnownRejection(t *testing.T) {
	for _, hint := range []bool{false, true} {
		req, result := historyProtocolFixture(t)
		root, done := historyAckPeer(t, "daemon.sock", func(r runtimeRequest) runtimeResponse {
			reply := runtimeResponse{Version: 1, ID: r.ID, Code: "changed"}
			if hint {
				h := recordHistoryHint(result.Hint)
				reply.HistoryHint = &h
			} else {
				reply.History = recordHistoryResult(req, result)
			}
			return reply
		})
		d, err := NewLocalDaemon(nil, DaemonOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = d.Launch(context.Background(), root, app.LaunchRequest{Epoch: req.Epoch, Worktree: req.Request.Worktree, Profile: "fixture", RequestID: "no-native-effects"})
		if !errors.Is(err, app.ErrLaunchUnknown) || !errors.Is(err, app.ErrRuntimeProtocol) {
			t.Fatal("new mixed arm became known launch refusal", err)
		}
		awaitHistoryAck(t, done)
	}
}

func TestHistoryHintCounterSaturatesWithoutNativeObservationQueue(t *testing.T) {
	// The service exists only as a capability marker; no core/agent port is invoked.
	s := &daemonServer{history: &app.History{}}
	s.historyHints.Store(^uint64(0) - 1)
	for range 3 {
		var reply runtimeResponse
		s.historyHintReply(&reply)
		if reply.Code != "ok" || reply.HistoryHint == nil {
			t.Fatal("hint lost")
		}
	}
	h := s.historyHintState()
	if !h.Valid() || !h.Overflow || h.Revision != ^uint64(0) {
		t.Fatal("counter wrapped", h)
	}
}
