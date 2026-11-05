package runtime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/domain/types"
)

func TestUpdateWireSingleArmAndCanonicalStatusRoundTrip(t *testing.T) {
	epoch := strings.Repeat("a", 32)
	target := domain.UpdateTarget{Version: "v2", Protocol: 2}
	digest, err := app.ShutdownRosterDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	binding := domain.ShutdownBindingState{Epoch: epoch, PreviewID: strings.Repeat("b", 32), Revision: 9007199254740993, RosterDigest: digest}
	request := lifecycleRequestRecord{UpdateAttempt: &updateAttemptRequestRecord{Epoch: epoch, Target: recordUpdateTarget(target)}}
	if !request.validOperation("update_attempt") || request.validOperation("update_status") {
		t.Fatal("update operation correlation lost")
	}
	request.Preview = &shutdownPreviewRequestRecord{Epoch: epoch}
	if request.validOperation("update_attempt") {
		t.Fatal("mixed lifecycle request accepted")
	}
	status := app.UpdateStatus{Epoch: epoch, Quiescent: true, Blockers: []string{}, Deferral: types.Some(domain.UpdateDeferralState{Target: target, Started: time.Now().UTC().Round(0), Notified24h: true}), Overdue24h: true}
	attempt := app.UpdateAttemptResult{Target: target, Status: status, Notice: true, Preview: types.Some(binding)}
	reply := runtimeResponse{Version: 1, ID: "fixture", Code: "ok", Lifecycle: &lifecycleResponseRecord{UpdateAttempt: recordUpdateAttempt(attempt)}}
	data, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	var decoded runtimeResponse
	if err = decodeRuntimeJSON(data, &decoded); err != nil {
		t.Fatal("canonical bounded update frame refused", err)
	}
	got, err := decoded.Lifecycle.UpdateAttempt.attempt()
	if err != nil {
		t.Fatal(err)
	}
	preview, ok := got.Preview.Value()
	if !ok || preview != binding || got.Target != target || !got.Valid() {
		t.Fatal("large revision/target correlation lost")
	}
	if !decoded.Lifecycle.singleArm() {
		t.Fatal("single update arm rejected")
	}
	record := recordShutdown(app.ShutdownResult{Binding: binding, Outcome: domain.ShutdownDraining})
	decoded.Lifecycle.Decision = &record
	if decoded.Lifecycle.singleArm() {
		t.Fatal("new/old mixed response accepted")
	}
	r := recordUpdateStatus(status)
	r.Deferral.Started = "2026-01-01T01:00:00+01:00"
	if _, err = r.status(); err == nil {
		t.Fatal("noncanonical deferral time accepted")
	}
}
