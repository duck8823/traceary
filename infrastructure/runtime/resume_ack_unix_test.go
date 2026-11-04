//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

func TestResumeSDKPrivateSocketCorrelatesOneOperationAndNeverReplaysUnknown(t *testing.T) {
	for _, mode := range []string{"confirmed", "stored", "unknown", "reserved", "no-start", "unsupported", "lost", "wrong-id", "wrong-request", "wrong-previous", "wrong-epoch", "same-run", "missing-owned", "missing-result", "wrong-phase", "mixed", "queued"} {
		t.Run(mode, func(t *testing.T) {
			request := resumeWireRequest()
			var calls atomic.Int32
			root, done := historyAckPeer(t, "daemon.sock", func(r runtimeRequest) runtimeResponse {
				calls.Add(1)
				if r.Operation != "resume" || r.Resume == nil || !r.singleArm() {
					t.Error("wrong operation/arm")
				}
				got, err := r.Resume.request()
				if err != nil || got != request {
					t.Error("lost original request")
				}
				next := request.Selection
				next.Identity = domain.LaunchIdentity{Run: strings.Repeat("4", 32), Generation: strings.Repeat("5", 32), Epoch: request.Epoch}
				result := app.ResumeResult{Selection: next, Phase: domain.ResumeConfirmed, Owned: true}
				reply := runtimeResponse{Version: 1, ID: r.ID, Code: "ok", Resume: recordResumeResult(request, result)}
				switch mode {
				case "stored":
					*reply.Resume.Owned = false
					reply.Resume.Selection.Epoch = request.Selection.Identity.Epoch
				case "unknown":
					reply.Code = "launch_unknown"
					reply.Resume.Phase = string(domain.ResumeUnknown)
				case "reserved":
					reply.Code = "launch_unknown"
					reply.Resume.Phase = string(domain.ResumeReserved)
					*reply.Resume.Owned = false
				case "no-start":
					reply.Code = "rejected"
					reply.Resume.Phase = string(domain.ResumeFailedBeforeStart)
					*reply.Resume.Owned = false
				case "unsupported":
					reply.Code = "unsupported"
					reply.Resume = nil
				case "lost":
					reply.ID = "lost"
				case "wrong-id":
					reply.ID = "other"
				case "wrong-request":
					reply.Resume.RequestID = "other"
				case "wrong-previous":
					reply.Resume.Previous.Generation = strings.Repeat("9", 32)
				case "wrong-epoch":
					reply.Resume.Selection.Epoch = request.Selection.Identity.Epoch
				case "same-run":
					reply.Resume.Selection.Run = request.Selection.Identity.Run
				case "missing-owned":
					reply.Resume.Owned = nil
				case "missing-result":
					reply.Resume = nil
				case "wrong-phase":
					reply.Resume.Phase = string(domain.ResumeUnknown)
				case "mixed":
					reply.Run = &launchRecord{}
				case "queued":
					reply.Code = "queued"
				}
				return reply
			})
			client, err := NewLocalDaemon(nil, DaemonOptions{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Resume(context.Background(), root, request)
			switch mode {
			case "confirmed":
				if err != nil || !result.Owned || result.Phase != domain.ResumeConfirmed {
					t.Fatalf("valid result refused: %+v %v", result, err)
				}
			case "stored":
				if err != nil || result.Owned || result.Phase != domain.ResumeConfirmed {
					t.Fatalf("stored result adopted: %+v %v", result, err)
				}
			case "no-start":
				if !errors.Is(err, app.ErrRuntimeRejected) || result.Owned || result.Phase != domain.ResumeFailedBeforeStart {
					t.Fatalf("no-start result changed: %+v %v", result, err)
				}
			case "unsupported":
				if !errors.Is(err, app.ErrRuntimeUnsupported) {
					t.Fatalf("known rejection changed: %v", err)
				}
			default:
				if !errors.Is(err, app.ErrLaunchUnknown) {
					t.Fatalf("unsafe known result: %+v %v", result, err)
				}
			}
			awaitHistoryAck(t, done)
			if calls.Load() != 1 {
				t.Fatal("resume was resent")
			}
		})
	}
}
func TestLegacyLaunchSDKRefusesResumeResponseEvenWithKnownRejectionCode(t *testing.T) {
	request := resumeWireRequest()
	root, done := historyAckPeer(t, "daemon.sock", func(r runtimeRequest) runtimeResponse {
		return runtimeResponse{Version: 1, ID: r.ID, Code: "invalid", Resume: recordResumeResult(request, app.ResumeResult{Phase: domain.ResumeUnknown})}
	})
	client, err := NewLocalDaemon(nil, DaemonOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Launch(context.Background(), root, app.LaunchRequest{Epoch: request.Epoch, Worktree: request.Selection.Worktree, Profile: request.Selection.Profile, RequestID: request.RequestID})
	if !errors.Is(err, app.ErrLaunchUnknown) || !errors.Is(err, app.ErrRuntimeProtocol) {
		t.Fatalf("foreign operation accepted as known rejection: %v", err)
	}
	awaitHistoryAck(t, done)
}
