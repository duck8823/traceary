package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application"
)

type payloadCodecInspectorStub struct {
	state application.PayloadCodecState
	err   error
}

func (s payloadCodecInspectorStub) InspectPayloadCodec(context.Context) (application.PayloadCodecState, error) {
	return s.state, s.err
}

func TestInspectPayloadCodecReportsCompressionAndDowngradeWarning(t *testing.T) {
	t.Setenv(cliLanguageEnvKey, "en")
	check := (&RootCLI{payloadCodecInspector: payloadCodecInspectorStub{state: application.PayloadCodecState{
		MetadataAvailable: true, MinimumReader: 34, EventBodyZstd: 2, AuditOutputZstd: 3,
	}}}).inspectPayloadCodec(context.Background(), storeFileSnapshot{Exists: true})
	if check.Status != doctorStatusWarn || !strings.Contains(check.Message, "v0.33") || !strings.Contains(check.Hint, "store backup") {
		t.Fatalf("check=%#v", check)
	}
	t.Setenv(cliLanguageEnvKey, "ja")
	check = (&RootCLI{payloadCodecInspector: payloadCodecInspectorStub{state: application.PayloadCodecState{
		MetadataAvailable: true, MinimumReader: 34, EventBodyZstd: 1,
	}}}).inspectPayloadCodec(context.Background(), storeFileSnapshot{Exists: true})
	if !strings.Contains(check.Message, "v0.33") || !strings.Contains(check.Hint, "store backup") {
		t.Fatalf("Japanese check=%#v", check)
	}
}

func TestInspectPayloadCodecFailsClosedWhenUnavailable(t *testing.T) {
	check := (&RootCLI{payloadCodecInspector: payloadCodecInspectorStub{err: errors.New("read failed")}}).inspectPayloadCodec(context.Background(), storeFileSnapshot{Exists: true})
	if check.Status != doctorStatusWarn || !strings.Contains(check.Message, "failed to inspect") {
		t.Fatalf("check=%#v", check)
	}
}
