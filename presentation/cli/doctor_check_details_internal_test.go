package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
)

// TestDoctorCheckDetails_SchemaSnapshot pins the public details schema for
// every codex-capture inspection state plus the codex host-capabilities row.
// Regenerate with `go test ./presentation/cli -run SchemaSnapshot -update`.
func TestDoctorCheckDetails_SchemaSnapshot(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	projectDir := t.TempDir()
	manual := codexPluginHookFallbackState{}
	plugin := codexPluginHookFallbackState{PluginEnabled: true, PluginKey: "traceary@traceary-marketplace"}
	trusted := codexPluginHookTrustResult{PluginKey: plugin.PluginKey, Status: codexPluginHookTrustTrusted}
	inspect := func(root *RootCLI, state codexPluginHookFallbackState, trust codexPluginHookTrustResult, spoolErr error) doctorCheck {
		return root.inspectCodexCapture(context.Background(), projectDir, "0.0.0-test", state, trust, nil, spoolErr)
	}

	partial := classifyCodexCapture(codexCaptureEvidence{SpoolPartial: true, StoredEvents: 2})
	hostChecks := inspectHostCapabilityGaps("codex", "/tmp/hooks.json")
	type snapshotEntry struct {
		Case    string              `json:"case"`
		Check   string              `json:"check"`
		Status  string              `json:"status"`
		Details *doctorCheckDetails `json:"details"`
	}
	var snapshot []snapshotEntry
	add := func(name string, check doctorCheck) {
		snapshot = append(snapshot, snapshotEntry{name, check.Name, check.Status, check.Details})
	}
	add("complete", inspect(&RootCLI{codexCaptureDiagnostic: &codexCaptureDiagnosticStub{evidence: apptypes.CodexCaptureDiagnosticEvidence{
		StoredEvents: 3, PromptTurns: 1, SessionStartObserved: true, PromptObserved: true,
		StopSessions: 1, StopSessionsWithUsage: 1, UsageObservations: 1, UsageKnown: 1,
	}}}, plugin, trusted, nil))
	add("empty_evidence", inspect(&RootCLI{codexCaptureDiagnostic: &codexCaptureDiagnosticStub{}}, manual, codexPluginHookTrustResult{Status: codexPluginHookTrustAbsent}, nil))
	add("partial", doctorCheck{
		Name: codexCaptureCheckName, Status: partial.Status,
		Details: newCodexCaptureInspectedDetails(codexCaptureIdentity{Surface: "manual_hooks", HookTrust: "absent"}, partial, codexCaptureEvidence{SpoolPartial: true, StoredEvents: 2}, 0),
	})
	add("read_failed", inspect(&RootCLI{codexCaptureDiagnostic: &codexCaptureDiagnosticStub{err: errors.New("boom")}}, plugin, trusted, nil))
	add("spool_read_failed", inspect(&RootCLI{codexCaptureDiagnostic: &codexCaptureDiagnosticStub{}}, plugin, trusted, errors.New("spool")))
	add("dependencies_unavailable", inspect(&RootCLI{}, plugin, trusted, nil))
	add("large_store_not_inspected", skippedLargeStoreCodexCaptureCheck(plugin, trusted))
	add("host_capabilities", hostChecks[0])

	got, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	fixture := filepath.Join("testdata", "doctor", "check-details.golden.json")
	if update := flag.Lookup("update"); update != nil && update.Value.String() == "true" {
		if err := os.WriteFile(fixture, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Fatalf("details schema snapshot mismatch (-want +got):\n%s", diff)
	}
}

// TestDoctorCheckDetails_UninspectedStatesCarryNoEvidence guards the
// "never zero-fill" contract: a diagnostic that did not inspect the store must
// not expose counts, boundaries, usage, or a window that read as observed.
func TestDoctorCheckDetails_UninspectedStatesCarryNoEvidence(t *testing.T) {
	t.Parallel()
	trust := codexPluginHookTrustResult{Status: codexPluginHookTrustAbsent}
	identity := codexCaptureIdentity{Surface: "manual_hooks", HookTrust: string(trust.Status)}
	tests := []struct {
		name           string
		check          doctorCheck
		wantInspection string
		wantStatus     string
	}{
		{"read failure", codexCaptureFailure(identity, "boom"), codexCaptureInspectionReadFailed, doctorStatusFail},
		{"dependencies unavailable", (&RootCLI{}).inspectCodexCapture(context.Background(), t.TempDir(), "", codexPluginHookFallbackState{}, trust, nil, nil), codexCaptureInspectionDependencies, doctorStatusWarn},
		{"large store", skippedLargeStoreCodexCaptureCheck(codexPluginHookFallbackState{}, trust), codexCaptureInspectionNotInspected, doctorStatusSkip},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.check.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", tt.check.Status, tt.wantStatus)
			}
			if tt.check.Details == nil || tt.check.Details.CodexCapture == nil {
				t.Fatalf("details = %#v, want codex_capture", tt.check.Details)
			}
			if got := tt.check.Details.CodexCapture.Inspection; got != tt.wantInspection {
				t.Fatalf("inspection = %q, want %q", got, tt.wantInspection)
			}
			raw, err := json.Marshal(tt.check.Details.CodexCapture)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{`"counts"`, `"boundaries"`, `"usage_state"`, `"window_seconds"`} {
				if strings.Contains(string(raw), key) {
					t.Fatalf("uninspected details expose %s: %s", key, raw)
				}
			}
		})
	}
}

// TestDoctorCheckDetails_ScopeMinimal asserts details never carry the
// workspace path, session identifiers, or any free-form text.
func TestDoctorCheckDetails_ScopeMinimal(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	root := &RootCLI{codexCaptureDiagnostic: &codexCaptureDiagnosticStub{evidence: apptypes.CodexCaptureDiagnosticEvidence{StoredEvents: 1}}}
	spool := []hookSpoolRecord{{
		Command: "hook-session-start", Client: "codex",
		Payload: `{"session_id":"session-secret-id","cwd":"` + projectDir + `","prompt":"private prompt body"}`,
	}}
	check := root.inspectCodexCapture(context.Background(), projectDir, "0.0.0-test", codexPluginHookFallbackState{}, codexPluginHookTrustResult{Status: codexPluginHookTrustAbsent}, spool, nil)
	raw, err := json.Marshal(check.Details)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"session-secret-id", "private prompt body", projectDir, "0.0.0-test"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("details leaked %q: %s", leaked, raw)
		}
	}
	if check.Details.CodexCapture.Counts.PendingDeliveries != 1 {
		t.Fatalf("pending_deliveries = %d, want 1 (spool record must still be counted)", check.Details.CodexCapture.Counts.PendingDeliveries)
	}
}

// TestDoctorCheckDetails_LanguageIndependent asserts English and Japanese
// output share identical machine fields while the prose differs.
func TestDoctorCheckDetails_LanguageIndependent(t *testing.T) {
	render := func(lang string) doctorCheck {
		t.Setenv("TRACEARY_LANG", lang)
		return inspectHostCapabilityGaps("codex", "/tmp/hooks.json")[0]
	}
	en := render("en")
	ja := render("ja")
	if en.Message == ja.Message {
		t.Fatalf("messages are identical; expected localized prose to differ: %s", en.Message)
	}
	if diff := cmp.Diff(en.Details, ja.Details); diff != "" {
		t.Fatalf("details differ between en and ja (-en +ja):\n%s", diff)
	}
}

// TestDoctorLargeStore_CodexCaptureIsExplicitlyNotInspected asserts the
// bounded large-store path reports codex-capture as an explicit skip instead
// of omitting it, so Desktop never reads absence as capture evidence.
func TestDoctorLargeStore_CodexCaptureIsExplicitlyNotInspected(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	setLargeStoreDoctorPath(t)
	t.Chdir(t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(ResetUserHomeDirFunc)

	rootCmd := newLargeStoreDoctorRootCLI(&trackingStoreStub{}, &trackingEventStub{}, &panicLargeStoreCapacityInspector{}).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--db-path", writeLargeStoreFixture(t), "--client", "codex", "--json", "--warnings-ok"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	report := decodeLargeStoreDoctorReport(t, stdout.Bytes())
	if report.Mode != doctorModeMetadataOnlyLargeStore {
		t.Fatalf("report.Mode = %q, want %s", report.Mode, doctorModeMetadataOnlyLargeStore)
	}
	var found bool
	for _, check := range report.Checks {
		switch check.Name {
		case codexCaptureCheckName:
			found = true
			if check.Status != doctorStatusSkip || check.Details == nil || check.Details.CodexCapture == nil ||
				check.Details.CodexCapture.Inspection != codexCaptureInspectionNotInspected || check.Details.CodexCapture.Counts != nil {
				t.Fatalf("codex-capture = %#v, want explicit not_inspected skip without counts", check)
			}
		case "codex-host-capabilities":
			if check.Details == nil || check.Details.HostCapabilities == nil {
				t.Fatalf("codex-host-capabilities details missing on large-store path: %#v", check)
			}
		}
	}
	if !found {
		t.Fatal("codex-capture check missing on large-store path")
	}
}
