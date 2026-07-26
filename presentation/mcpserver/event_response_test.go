package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	anchor, err := apptypes.EventPageAnchorOf(
		time.Date(2026, 7, 25, 23, 59, 0, 0, time.UTC),
		types.EventID("private-event-id"),
	)
	if err != nil {
		t.Fatalf("EventPageAnchorOf: %v", err)
	}
	cursor, err := encodeEventContinuation(request, anchor, "2026-07-26T00:00:00.000000000Z")
	if err != nil {
		t.Fatalf("encodeEventContinuation: %v", err)
	}
	if strings.Contains(cursor, "private-event-id") || strings.Contains(cursor, "workspace") {
		t.Fatalf("continuation exposed private criteria or event ID: %s", cursor)
	}
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

	encoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatalf("decode opaque continuation: %v", err)
	}
	encoded[len(encoded)-1] ^= 0x01
	modified := base64.RawURLEncoding.EncodeToString(encoded)
	if _, err := resolveEventPageRequest("list_events", 20, 0, modified, fingerprint, 500); err == nil {
		t.Fatal("modified continuation was accepted")
	}

	for _, incomplete := range []eventContinuationCursor{
		{Version: eventContinuationVersion, Tool: "list_events", Fingerprint: fingerprint, AnchorCreatedAt: "2026-07-25T23:59:00Z", AnchorEventID: "event-1"},
		{Version: eventContinuationVersion, Tool: "list_events", Fingerprint: fingerprint, Snapshot: "2026-07-26T00:00:00Z", AnchorEventID: "event-1"},
		{Version: eventContinuationVersion, Tool: "list_events", Fingerprint: fingerprint, Snapshot: "2026-07-26T00:00:00Z", AnchorCreatedAt: "2026-07-25T23:59:00Z"},
	} {
		raw := encryptEventContinuationFixture(t, incomplete)
		if _, err := resolveEventPageRequest("list_events", 20, 0, raw, fingerprint, 500); err == nil {
			t.Fatalf("incomplete continuation was accepted: %+v", incomplete)
		}
	}
}

func TestListEventsContinuationRejectsChangedNormalizedPageLimit(t *testing.T) {
	t.Parallel()

	metadata := newMCPMetadataFixture(t)
	metadataUsecase := &projectionMetadataUsecaseStub{
		list: make([]apptypes.EventMetadata, apptypes.DefaultEventResponseItemLimit+1),
	}
	for i := range metadataUsecase.list {
		metadataUsecase.list[i] = metadata
	}
	server := &Server{eventMetadata: metadataUsecase}
	_, first, err := server.listEvents()(
		context.Background(),
		nil,
		listEventsInput{Projection: "metadata", Workspace: " repo "},
	)
	if err != nil {
		t.Fatalf("first listEvents() error = %v", err)
	}
	if first.Continuation == "" {
		t.Fatal("first listEvents() continuation is empty")
	}
	if _, _, err := server.listEvents()(
		context.Background(),
		nil,
		listEventsInput{
			Projection: "metadata", Limit: apptypes.DefaultEventResponseItemLimit, Workspace: "repo",
			Continuation: first.Continuation,
		},
	); err != nil {
		t.Fatalf("continuation with the explicit default page limit was rejected: %v", err)
	}
	if _, _, err := server.listEvents()(
		context.Background(),
		nil,
		listEventsInput{
			Projection: "metadata", Limit: apptypes.DefaultEventResponseItemLimit - 1, Workspace: "repo",
			Continuation: first.Continuation,
		},
	); err == nil {
		t.Fatal("continuation with a changed normalized page limit was accepted")
	}
}

func TestLoadEventPageCapsAggregateBodiesAndReturnsContinuation(t *testing.T) {
	t.Parallel()
	budget, err := apptypes.NewEventResponseBudget(5, 0)
	if err != nil {
		t.Fatalf("NewEventResponseBudget: %v", err)
	}
	request := eventPageRequest{
		budget: budget, tool: "list_events", fingerprint: eventRequestFingerprint("list_events"),
		snapshot: "2026-07-26T00:00:00.000000000Z",
	}
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
	}, nil, request.snapshot)
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

func encryptEventContinuationFixture(t *testing.T, cursor eventContinuationCursor) string {
	t.Helper()
	plaintext, err := json.Marshal(cursor)
	if err != nil {
		t.Fatalf("json.Marshal(cursor): %v", err)
	}
	aead, err := loadEventContinuationAEAD()
	if err != nil {
		t.Fatalf("loadEventContinuationAEAD: %v", err)
	}
	nonce := make([]byte, aead.NonceSize())
	encoded := aead.Seal(nonce, nonce, plaintext, []byte(eventContinuationAdditionalData))
	return base64.RawURLEncoding.EncodeToString(encoded)
}
