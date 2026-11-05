//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// Real peer transport but no runtime/process effects: tests ACK correlation and
// no automatic resend, not native host/update installation qualification.
func updatePeer(t *testing.T, makeReply func(runtimeRequest) runtimeResponse, call func(*LocalDaemon, string)) {
	t.Helper()
	root := receiptRoot(t)
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
		reply := makeReply(request)
		if reply.Code != "lost" {
			reply.Version = 1
			if reply.ID == "" {
				reply.ID = request.ID
			}
			e = writeRuntimeFrame(conn, reply)
		}
		done <- e
	}()
	d, err := NewLocalDaemon(nil, DaemonOptions{})
	if err != nil {
		t.Fatal(err)
	}
	call(d, root)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestUpdateAttemptLostMixedWrongAndQueuedAckNeverBecomesKnown(t *testing.T) {
	for _, mode := range []string{"lost", "mixed", "wrong-id", "wrong-target", "wrong-epoch", "queued", "missing-preview", "delivered", "known-reject", "uncertain"} {
		t.Run(mode, func(t *testing.T) {
			epoch := strings.Repeat("a", 32)
			target := domain.UpdateTarget{Version: "v2", Protocol: 2}
			updatePeer(t, func(request runtimeRequest) runtimeResponse {
				if request.Operation != "update_attempt" {
					t.Error("wrong SDK operation")
				}
				status := app.UpdateStatus{Epoch: epoch, Quiescent: false, Blockers: []string{"active_runs"}}
				result := app.UpdateAttemptResult{Target: target, Status: status, Notice: true}
				reply := runtimeResponse{Code: "ok", Lifecycle: &lifecycleResponseRecord{UpdateAttempt: recordUpdateAttempt(result)}}
				switch mode {
				case "lost":
					reply.Code = "lost"
				case "wrong-id":
					reply.ID = "foreign-response"
					reply.Code = "busy"
				case "mixed":
					reply.Lifecycle.UpdateStatus = recordUpdateStatus(status)
				case "wrong-target":
					reply.Lifecycle.UpdateAttempt.Target.Version = "v3"
				case "wrong-epoch":
					reply.Lifecycle.UpdateAttempt.Status.Epoch = strings.Repeat("b", 32)
				case "queued":
					reply.Code = "queued"
				case "missing-preview":
					reply.Lifecycle.UpdateAttempt.Status.Quiescent = true
					reply.Lifecycle.UpdateAttempt.Status.Blockers = []string{}
				case "known-reject":
					reply.Code = "busy"
				case "uncertain":
					reply.Code = "update_unknown"
					reply.Lifecycle.UpdateAttempt.Status.Blockers = []string{"publication_uncertain", "admission_closed"}
				}
				return reply
			}, func(d *LocalDaemon, root string) {
				result, err := d.AttemptUpdate(context.Background(), root, app.UpdateAttemptRequest{Epoch: epoch, Target: target})
				switch mode {
				case "delivered":
					if err != nil || !result.Notice {
						t.Fatal("valid deferred ACK refused", err)
					}
				case "known-reject":
					if !errors.Is(err, app.ErrRuntimeBusy) || errors.Is(err, app.ErrUpdateUnknown) || !result.Notice {
						t.Fatal("known refusal lost notice/certainty", err)
					}
				default:
					if !errors.Is(err, app.ErrUpdateUnknown) {
						t.Fatal("possibly accepted attempt classified known", err)
					}
				}
			})
		})
	}
}

func TestUpdateHandoffLostOrContradictoryAckNeverReplays(t *testing.T) {
	epoch := strings.Repeat("a", 32)
	target := domain.UpdateTarget{Version: "v2", Protocol: 2}
	digest, err := app.ShutdownRosterDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	binding := domain.ShutdownBindingState{Epoch: epoch, PreviewID: strings.Repeat("b", 32), Revision: 3, RosterDigest: digest}
	for _, mode := range []string{"lost", "wrong-target", "mixed", "pending", "queued", "contradictory", "delivered"} {
		t.Run(mode, func(t *testing.T) {
			updatePeer(t, func(request runtimeRequest) runtimeResponse {
				if request.Operation != "update_handoff" {
					t.Error("wrong SDK operation")
				}
				r := recordShutdown(app.ShutdownResult{Binding: binding, Outcome: domain.ShutdownDraining})
				reply := runtimeResponse{Code: "ok", Lifecycle: &lifecycleResponseRecord{UpdateHandoff: &updateHandoffResultRecord{Target: recordUpdateTarget(target), Decision: r}}}
				switch mode {
				case "lost":
					reply.Code = "lost"
				case "wrong-target":
					reply.Lifecycle.UpdateHandoff.Target.Version = "v3"
				case "mixed":
					reply.Lifecycle.Decision = &r
				case "pending":
					reply.Lifecycle.UpdateHandoff.Decision.Outcome = string(domain.ShutdownPending)
				case "queued":
					reply.Code = "queued"
				case "contradictory":
					reply.Code = "changed"
				}
				return reply
			}, func(d *LocalDaemon, root string) {
				result, e := d.HandoffUpdate(context.Background(), root, app.UpdateHandoffRequest{Target: target, Binding: binding, DaemonExitAcknowledged: true})
				if mode == "delivered" {
					if e != nil || result.Outcome != domain.ShutdownDraining {
						t.Fatal(e)
					}
				} else if !errors.Is(e, app.ErrShutdownUnknown) || result.Outcome != domain.ShutdownUnknown {
					t.Fatal("uncertain handoff became known", e)
				}
			})
		})
	}
}

func TestLifecycleCommitUncertaintyOutranksJoinedKnownRejection(t *testing.T) {
	if code := operationCode(errors.Join(app.ErrLifecycleCommitUncertain, domain.ErrLimit)); code != "update_unknown" {
		t.Fatalf("uncertain write classified known rejection: %s", code)
	}
}

func TestOldShutdownSDKRejectsMixedNewUpdateResponseArm(t *testing.T) {
	epoch := strings.Repeat("a", 32)
	digest, err := app.ShutdownRosterDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	binding := domain.ShutdownBindingState{Epoch: epoch, PreviewID: strings.Repeat("b", 32), Revision: 3, RosterDigest: digest}
	updatePeer(t, func(runtimeRequest) runtimeResponse {
		decision := recordShutdown(app.ShutdownResult{Binding: binding, Outcome: domain.ShutdownDraining})
		return runtimeResponse{Code: "ok", Lifecycle: &lifecycleResponseRecord{Decision: &decision, UpdateStatus: recordUpdateStatus(app.UpdateStatus{Epoch: epoch, Quiescent: true, Blockers: []string{}})}}
	}, func(d *LocalDaemon, root string) {
		result, e := d.ConfirmShutdown(context.Background(), root, app.ShutdownConfirmation{Binding: binding, WorkLossAcknowledged: true})
		if !errors.Is(e, app.ErrShutdownUnknown) || result.Outcome != domain.ShutdownUnknown {
			t.Fatal("old operation accepted mixed new update arm", e)
		}
	})
}
