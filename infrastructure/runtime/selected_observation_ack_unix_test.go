//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
)

func TestSelectedObservationSDKRejectsEveryMixedAndUncertainAckWithoutReplay(t *testing.T) {
	for _, mode := range []string{"queued", "lost", "wrong_id", "old_invalid", "unsupported", "busy", "ok", "role", "history", "hint", "control", "output", "status", "run", "page"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			root, done := historyAckPeer(t, "observations.sock", func(request runtimeRequest) runtimeResponse {
				calls.Add(1)
				if request.Operation != selectedObservationOperation || request.SelectedObservation == nil {
					t.Error("SDK used an unexpected operation/arm")
				}
				reply := runtimeResponse{Version: 1, ID: request.ID, Code: "queued"}
				switch mode {
				case "lost":
					reply.ID = "lost"
				case "wrong_id":
					reply.ID = "different"
				case "old_invalid":
					reply.ID = ""
					reply.Code = "invalid"
				case "unsupported":
					reply.Code = "unsupported"
				case "busy":
					reply.Code = "busy"
				case "ok":
					reply.Code = "ok"
				case "role":
					reply.Role = "observations"
				case "history":
					reply.History = &historyResultRecord{}
				case "hint":
					reply.HistoryHint = &historyHintRecord{Revision: "1", Overflow: new(bool)}
				case "control":
					reply.Control = &controlResultRecord{}
				case "output":
					reply.Output = &outputRecord{}
				case "status":
					reply.Status = &runtimeStatusRecord{Version: 1, Epoch: strings.Repeat("e", 32), State: "listening", Capabilities: []string{"status"}, ObservationDelivery: "configured"}
				case "run":
					reply.Run = &launchRecord{}
				case "page":
					reply.Page = &runPageRecord{}
				}
				return reply
			})
			client, err := NewLocalDaemon(nil, DaemonOptions{})
			if err != nil {
				t.Fatal(err)
			}
			err = client.ObserveSelected(context.Background(), root, selectedProtocolFixture(t))
			var expected error
			switch mode {
			case "queued":
			case "lost":
				expected = app.ErrDaemonUnavailable
			case "unsupported":
				expected = app.ErrRuntimeUnsupported
			case "busy":
				expected = app.ErrRuntimeBusy
			default:
				expected = app.ErrRuntimeProtocol
			}
			if expected == nil {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, expected) {
				t.Fatalf("ACK %s error=%v want %v", mode, err, expected)
			}
			awaitHistoryAck(t, done)
			if calls.Load() != 1 {
				t.Fatal("optional metadata was replayed")
			}
		})
	}
}

// Serial: this observes the process-global retained transport slots.
func TestSelectedObservationSDKTotalBudgetIncludesHandshakeAndAck(t *testing.T) {
	for _, tt := range []struct {
		name       string
		hello, ack time.Duration
		requests   int32
	}{
		{"hello", 350 * time.Millisecond, 0, 0},
		{"ack", 0, 350 * time.Millisecond, 1},
		{"combined", 130 * time.Millisecond, 180 * time.Millisecond, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			root, done := historyAckPeer(t, "observations.sock", func(request runtimeRequest) runtimeResponse {
				requests.Add(1)
				time.Sleep(tt.ack)
				return runtimeResponse{Version: 1, ID: request.ID, Code: "queued"}
			}, func() { time.Sleep(tt.hello) })
			client, err := NewLocalDaemon(nil, DaemonOptions{})
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			err = client.ObserveSelected(context.Background(), root, selectedProtocolFixture(t))
			elapsed := time.Since(started)
			if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, app.ErrDaemonUnavailable) {
				t.Fatalf("optional budget error=%v", err)
			}
			if elapsed > time.Second {
				t.Fatalf("optional total budget exceeded scheduler watchdog: %v", elapsed)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("bounded peer did not reconcile after caller deadline")
			}
			if requests.Load() != tt.requests {
				t.Fatalf("fixture request count=%d want %d", requests.Load(), tt.requests)
			}
			deadline := time.Now().Add(time.Second)
			for len(runtimeClientSlots) != 0 {
				if time.Now().After(deadline) {
					t.Fatal("timed-out selected exchange retained a worker slot")
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}
