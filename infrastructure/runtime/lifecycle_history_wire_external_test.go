//go:build darwin || linux

package runtime_test

import (
	"context"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"testing"
)

func TestDaemonRejectsHistoryHintMixedWithLifecycleBeforeAdmission(t *testing.T) {
	root := daemonRoot(t)
	d := daemon(t, nil, infra.DaemonOptions{HistoryFactory: infra.NewHistoryService})
	_, _ = startDaemon(t, d, root)
	conn := rawEndpoint(t, root, "observations.sock", "observations")
	rawFrame(t, conn, `{"protocol_version":1,"request_id":"mixed","operation":"history_hint","history_hint":{},"lifecycle":{}}`)
	if code := rawCode(t, conn); code != "invalid" {
		t.Fatal("mixed history/lifecycle request admitted", code)
	}
	if err := d.HintHistory(context.Background(), root); err != nil {
		t.Fatal("clean hint failed", err)
	}
}
