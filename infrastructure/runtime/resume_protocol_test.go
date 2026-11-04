package runtime

import (
	"encoding/json"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"strings"
	"testing"
)

func resumeWireRequest() app.ResumeRequest {
	return app.ResumeRequest{Epoch: strings.Repeat("e", 32), Selection: outputWireSelection(), RequestID: "resume-original", WorkLossAcknowledged: true}
}
func TestResumeWireClosedCorrelationAndOwnership(t *testing.T) {
	request := resumeWireRequest()
	record := recordResumeRequest(request)
	got, err := record.request()
	if err != nil || got != request {
		t.Fatalf("request round trip: %v", err)
	}
	next := request.Selection
	next.Identity = domain.LaunchIdentity{Run: strings.Repeat("4", 32), Generation: strings.Repeat("5", 32), Epoch: request.Epoch}
	result := app.ResumeResult{Selection: next, Phase: domain.ResumeConfirmed, Owned: true}
	response := recordResumeResult(request, result)
	gotResult, err := response.result(request)
	if err != nil || gotResult != result {
		t.Fatalf("result round trip: %v", err)
	}
	response.Owned = nil
	if _, err := response.result(request); err == nil {
		t.Fatal("missing ownership accepted")
	}
	response = recordResumeResult(request, result)
	response.RequestID = "other"
	if _, err := response.result(request); err == nil {
		t.Fatal("wrong original request accepted")
	}
	response = recordResumeResult(request, result)
	response.Selection.Epoch = request.Selection.Identity.Epoch
	if _, err := response.result(request); err == nil {
		t.Fatal("historical selection adopted as current owner")
	}
	mixed := runtimeRequest{Operation: "resume", Resume: record, Launch: &launchRequestRecord{}}
	if mixed.singleArm() {
		t.Fatal("mixed resume/launch request admitted")
	}
	data, err := json.Marshal(runtimeRequest{Version: 1, ID: "transport", Operation: "resume", Resume: record})
	if err != nil {
		t.Fatal(err)
	}
	var decoded runtimeRequest
	if err := decodeRuntimeJSON(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{`"resume":`, `"Resume":`}, {`"work_loss":true`, `"work_loss":null`}, {`"selection":`, `"Selection":`}, {`"work_loss":true`, `"work_loss":true,"input":"forbidden"`}} {
		invalid := strings.Replace(string(data), pair[0], pair[1], 1)
		if err := decodeRuntimeJSON([]byte(invalid), &decoded); err == nil {
			t.Fatalf("ambiguous resume request accepted: %s", pair[0])
		}
	}
}

func TestResumeWireUnknownOutcomeNeverInventsSuccessorOrOwner(t *testing.T) {
	request := resumeWireRequest()
	unknown := recordResumeResult(request, app.ResumeResult{Phase: domain.ResumeUnknown})
	result, err := unknown.result(request)
	if err != nil || result.Owned || result.Selection != (app.OutputSelection{}) {
		t.Fatalf("unknown result invented owner: %+v %v", result, err)
	}
	*unknown.Owned = true
	if _, err := unknown.result(request); err == nil {
		t.Fatal("owner without known successor accepted")
	}
	*unknown.Owned = false
	unknown.Phase = string(domain.ResumeConfirmed)
	if _, err := unknown.result(request); err == nil {
		t.Fatal("confirmation without known successor accepted")
	}
	request.WorkLossAcknowledged = false
	if request.Valid() {
		t.Fatal("missing loss consent accepted")
	}
	request = resumeWireRequest()
	request.Selection.Worktree = domain.WorktreeID("w_" + strings.Repeat("A", 64))
	if request.Valid() {
		t.Fatal("noncanonical worktree reference accepted")
	}
}
