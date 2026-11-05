package runtime_test

import (
	"strings"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/domain/types"
)

func TestUpdateClientModelsRejectContradictoryAndUnboundedMetadata(t *testing.T) {
	epoch := strings.Repeat("a", 32)
	target := domain.UpdateTarget{Version: "v2", Protocol: 2}
	status := app.UpdateStatus{Epoch: epoch, Quiescent: true, Blockers: []string{}}
	if !status.Valid() {
		t.Fatal("valid idle metadata refused")
	}
	for _, change := range []func(*app.UpdateStatus){
		func(s *app.UpdateStatus) { s.Blockers = nil }, func(s *app.UpdateStatus) { s.Blockers = []string{"active_runs"} },
		func(s *app.UpdateStatus) { s.Quiescent = false }, func(s *app.UpdateStatus) { s.Overdue24h = true },
		func(s *app.UpdateStatus) { s.Quiescent = false; s.Blockers = []string{"active_runs", "active_runs"} },
		func(s *app.UpdateStatus) { s.Quiescent = false; s.Blockers = []string{"invented"} },
		func(s *app.UpdateStatus) {
			s.Quiescent = false
			s.Blockers = []string{"admission_closed", "active_runs"}
		},
	} {
		bad := status
		change(&bad)
		if bad.Valid() {
			t.Fatalf("contradictory status accepted: %+v", bad)
		}
	}
	status.Deferral = types.Some(domain.UpdateDeferralState{Target: target, Started: time.Now().UTC().Round(0), Notified24h: true})
	status.Overdue24h = true
	if !status.Valid() {
		t.Fatal("published overdue metadata refused")
	}
	digest, err := app.ShutdownRosterDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	binding := domain.ShutdownBindingState{Epoch: epoch, PreviewID: strings.Repeat("b", 32), Revision: 9007199254740993, RosterDigest: digest}
	result := app.UpdateAttemptResult{Target: target, Status: status, Notice: true, Preview: types.Some(binding)}
	if !result.Valid() {
		t.Fatal("valid attempt metadata refused")
	}
	bad := result
	bad.Notice = false
	if bad.Valid() {
		t.Fatal("attempt silently omitted notice")
	}
	bad = result
	bad.Status.Quiescent = false
	bad.Status.Blockers = []string{"active_runs"}
	if bad.Valid() {
		t.Fatal("blocked attempt carried empty-ready preview")
	}
	request := app.UpdateHandoffRequest{Target: target, Binding: binding, DaemonExitAcknowledged: true}
	if !request.Valid() {
		t.Fatal("valid empty handoff request refused")
	}
	request.Binding.TotalRuns = 1
	if request.Valid() {
		t.Fatal("handoff accepted live roster")
	}
	request.Binding = binding
	request.DaemonExitAcknowledged = false
	if request.Valid() {
		t.Fatal("handoff omitted explicit consent")
	}
}
