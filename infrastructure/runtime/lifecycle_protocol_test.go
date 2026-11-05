package runtime

import (
	"encoding/json"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"strings"
	"testing"
)

func TestLifecycleWireRejectsMixedIntentAndNoncanonicalBinding(t *testing.T) {
	binding := domain.ShutdownBindingState{Epoch: strings.Repeat("a", 32), PreviewID: strings.Repeat("b", 32), Revision: 9007199254740993, RosterDigest: strings.Repeat("c", 64)}
	valid := lifecycleRequestRecord{Confirm: &shutdownConfirmationRecord{Binding: recordShutdownBinding(binding), WorkLossAcknowledged: true}}
	if !valid.validOperation("shutdown_confirm") {
		t.Fatal("valid confirmation refused")
	}
	data, err := json.Marshal(runtimeRequest{Version: 1, ID: "test", Operation: "shutdown_confirm", Lifecycle: &valid})
	if err != nil {
		t.Fatal(err)
	}
	var decoded runtimeRequest
	if err = decodeRuntimeJSON(data, &decoded); err != nil {
		t.Fatal(err)
	}
	got, err := decoded.Lifecycle.Confirm.Binding.binding()
	if err != nil || got.Snapshot() != binding {
		t.Fatal("binding counters lost precision")
	}
	invalid := valid
	invalid.Cancel = &valid.Confirm.Binding
	if invalid.validOperation("shutdown_confirm") {
		t.Fatal("mixed lifecycle intent accepted")
	}
	invalid = valid
	copyConfirm := *valid.Confirm
	copyConfirm.Binding.Revision = "09007199254740993"
	invalid.Confirm = &copyConfirm
	if invalid.validOperation("shutdown_confirm") {
		t.Fatal("noncanonical binding accepted")
	}
	invalid = valid
	copyConfirm = *valid.Confirm
	copyConfirm.WorkLossAcknowledged = false
	invalid.Confirm = &copyConfirm
	if invalid.validOperation("shutdown_confirm") {
		t.Fatal("unacknowledged work loss accepted")
	}
	if valid.validOperation("shutdown_cancel") {
		t.Fatal("operation/payload mismatch accepted")
	}
	page := app.ShutdownPreviewPage{Binding: binding, Runs: []app.ShutdownPreviewRun{}}
	record := recordShutdownPage(page)
	restored, err := record.page()
	if err != nil || restored.Binding != binding || restored.Runs == nil {
		t.Fatal("empty complete page not canonical", err)
	}
	record.Next = strings.Repeat("d", 32)
	if _, err = record.page(); err == nil {
		t.Fatal("empty nonterminal page accepted")
	}
}
