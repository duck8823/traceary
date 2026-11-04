//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	"testing"
)

func TestHistorySDKRejectsLifecycleContaminatedHintAck(t *testing.T) {
	root, done := historyAckPeer(t, "observations.sock", func(r runtimeRequest) runtimeResponse {
		return runtimeResponse{Version: 1, ID: r.ID, Code: "ok", HistoryHint: &historyHintRecord{Revision: "1", Overflow: new(bool)}, Lifecycle: &lifecycleResponseRecord{}}
	})
	d, err := NewLocalDaemon(nil, DaemonOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.HintHistory(context.Background(), root); !errors.Is(err, app.ErrRuntimeProtocol) {
		t.Error("contaminated ack reported successful", err)
	}
	awaitHistoryAck(t, done)
}
