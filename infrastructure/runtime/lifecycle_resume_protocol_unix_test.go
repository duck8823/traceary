//go:build darwin || linux

package runtime

import "testing"

func TestLifecycleReplyRejectsRecoveryDataEvenOnKnownRefusal(t *testing.T) {
	if lifecycleOnlyReply(runtimeResponse{Code: "unsupported", Resume: &resumeResultRecord{}}) {
		t.Fatal("foreign resume data accepted as a lifecycle refusal")
	}
}

func TestObservationAcknowledgmentCannotCarryLifecycleData(t *testing.T) {
	if (runtimeResponse{Code: "queued", Lifecycle: &lifecycleResponseRecord{}}).acknowledgmentOnly() {
		t.Fatal("lifecycle data became an admission-only observation acknowledgment")
	}
}

func TestRuntimeRequestNeverCombinesLifecycleAndResume(t *testing.T) {
	if (runtimeRequest{Operation: "resume", Resume: &resumeRequestRecord{}, Lifecycle: &lifecycleRequestRecord{}}).singleArm() {
		t.Fatal("lifecycle and recovery effects shared one request")
	}
}
