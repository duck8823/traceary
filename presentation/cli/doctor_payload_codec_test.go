package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application"
)

type payloadCodecInspectorStub struct {
	state application.PayloadCodecState
	err   error
}

func (s payloadCodecInspectorStub) InspectPayloadCodec(context.Context) (application.PayloadCodecState, error) {
	return s.state, s.err
}

func TestInspectPayloadCodecReportsCompressedRowsWithoutWarning(t *testing.T) {
	tests := []struct {
		name        string
		language    string
		state       application.PayloadCodecState
		wantMessage string
	}{
		{name: "English", language: "en", state: application.PayloadCodecState{MetadataAvailable: true, CompatibilityMode: "counter", CompatibilityState: "valid", EventBodyNonIdentity: 2, AuditOutputNonIdentity: 3}, wantMessage: "payload codec has compressed rows (events.body=2, command_audits command=0 input=0 output=3); downgrade to v0.33 or earlier cannot read them"},
		{name: "Japanese", language: "ja", state: application.PayloadCodecState{MetadataAvailable: true, CompatibilityMode: "counter", CompatibilityState: "valid", EventBodyNonIdentity: 1}, wantMessage: "payload codec に圧縮行があります（events.body=1、command_audits command=0 input=0 output=0）。v0.33 以前へ downgrade すると読み取れません"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(cliLanguageEnvKey, tt.language)
			check := (&RootCLI{payloadCodecInspector: payloadCodecInspectorStub{state: tt.state}}).inspectPayloadCodec(context.Background(), storeFileSnapshot{Exists: true})
			if diff := cmp.Diff(doctorStatusPass, check.Status); diff != "" {
				t.Fatalf("status mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantMessage, check.Message); diff != "" {
				t.Fatalf("message mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff("", check.Hint); diff != "" {
				t.Fatalf("hint mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestInspectPayloadCodecReportsInvalidCompatibilityEvidence(t *testing.T) {
	check := (&RootCLI{payloadCodecInspector: payloadCodecInspectorStub{state: application.PayloadCodecState{
		MetadataAvailable: true, CompatibilityMode: "counter", CompatibilityState: "invalid", EventBodyNonIdentity: 999,
	}}}).inspectPayloadCodec(context.Background(), storeFileSnapshot{Exists: true})
	if diff := cmp.Diff(doctorStatusWarn, check.Status); diff != "" {
		t.Fatalf("status mismatch (-want +got):\n%s", diff)
	}
	want := "payload codec compatibility evidence is invalid (mode=counter); compressed-row counts are unavailable"
	if diff := cmp.Diff(want, check.Message); diff != "" {
		t.Fatalf("message mismatch (-want +got):\n%s", diff)
	}
}

func TestInspectPayloadCodecFailsClosedWhenUnavailable(t *testing.T) {
	check := (&RootCLI{payloadCodecInspector: payloadCodecInspectorStub{err: errors.New("read failed")}}).inspectPayloadCodec(context.Background(), storeFileSnapshot{Exists: true})
	if diff := cmp.Diff(doctorStatusWarn, check.Status); diff != "" {
		t.Fatalf("status mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("failed to inspect payload codec state: read failed", check.Message); diff != "" {
		t.Fatalf("message mismatch (-want +got):\n%s", diff)
	}
}
