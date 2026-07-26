package mcpserver

import (
	"context"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestResolveEventPageRequestRejectsUnsafeContinuations(t *testing.T) {
	t.Parallel()
	fingerprint := eventRequestFingerprint("list_events", "workspace")
	request, err := resolveEventPageRequest("list_events", 20, 0, "", fingerprint, 500)
	if err != nil {
		t.Fatalf("resolve initial request: %v", err)
	}
	cursor := encodeEventContinuation(request, 20, "2026-07-26T00:00:00Z")
	if _, err := resolveEventPageRequest("search", 20, 0, cursor, fingerprint, 500); err == nil {
		t.Fatal("cross-tool continuation was accepted")
	}
	if _, err := resolveEventPageRequest("list_events", 20, 1, cursor, fingerprint, 500); err == nil {
		t.Fatal("offset with continuation was accepted")
	}
	if _, err := resolveEventPageRequest("list_events", 20, 0, cursor, eventRequestFingerprint("list_events", "other"), 500); err == nil {
		t.Fatal("mismatched continuation was accepted")
	}
	if _, err := resolveEventPageRequest("list_events", apptypes.MaxEventResponseItemLimit+1, 0, "", fingerprint, 500); err == nil {
		t.Fatal("over-limit page was accepted")
	}
}

func TestLoadEventPageCapsAggregateBodiesAndReturnsContinuation(t *testing.T) {
	t.Parallel()
	budget, err := apptypes.NewEventResponseBudget(5, 0)
	if err != nil {
		t.Fatalf("NewEventResponseBudget: %v", err)
	}
	request := eventPageRequest{budget: budget, tool: "list_events", fingerprint: eventRequestFingerprint("list_events")}
	metadata := make([]apptypes.EventMetadata, 5)
	full := make([]*model.Event, 5)
	for i := range full {
		metadata[i] = newMCPMetadataFixture(t)
		full[i] = model.EventOf(types.EventID("budget-event-"+string(rune('a'+i))), types.EventKindNote, types.Client("hook"), types.Agent("codex"), types.SessionID("session"), types.Workspace("repo"), strings.Repeat("x", 20000), time.Now().UTC())
	}
	output, err := loadEventPage(context.Background(), request, apptypes.EventProjectionFull, eventPageLoaders{
		metadataAvailable: true,
		candidates:        func(context.Context, int, int) ([]apptypes.EventMetadata, error) { return metadata, nil },
		full:              func(context.Context, int, int) ([]*model.Event, error) { return full, nil },
		convertFull:       func(events []*model.Event) []eventOutput { return convertEventsWithBodyLimit(events, 0) },
	}, nil, "")
	if err != nil {
		t.Fatalf("loadEventPage: %v", err)
	}
	if !output.Partial || output.Continuation == "" {
		t.Fatalf("output must be partial with continuation: %+v", output)
	}
	if output.Coverage.AggregateBodyBytes > apptypes.MaxEventResponseAggregateBodyBytes {
		t.Fatalf("aggregate bytes = %d", output.Coverage.AggregateBodyBytes)
	}
	if len(output.Events) != len(full) {
		t.Fatalf("returned events = %d, want %d", len(output.Events), len(full))
	}
	truncated := false
	for _, event := range output.Events {
		truncated = truncated || event.BodyTruncated || event.Body == nil
	}
	if !truncated {
		t.Fatalf("aggregate cap did not reduce a body: %+v", output.Events)
	}
}
