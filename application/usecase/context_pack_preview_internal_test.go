package usecase

import (
	"context"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type commandPreviewQueryStub struct {
	previews       []apptypes.EventBodyPreview
	requestedRunes int
}

func (s *commandPreviewQueryStub) ListRecentCommandPreviews(_ context.Context, _ domtypes.SessionID, _, bodyRuneLimit int) ([]apptypes.EventBodyPreview, error) {
	s.requestedRunes = bodyRuneLimit
	return s.previews, nil
}

func TestContextPackBuilder_LoadRecentCommandsUsesBoundedPreview(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 22, 3, 0, 0, 0, time.UTC)
	preview, err := apptypes.EventBodyPreviewOf(
		domtypes.EventID("event-command"), "go    test ./...\n\nfull output that is not returned",
		10_000, domtypes.Some(12_000), domtypes.Some(true), domtypes.Some(false), now,
	)
	if err != nil {
		t.Fatalf("EventBodyPreviewOf() error = %v", err)
	}
	previewQuery := &commandPreviewQueryStub{previews: []apptypes.EventBodyPreview{preview}}
	builder := &contextPackBuilder{previewQuery: previewQuery}
	session := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-1"), domtypes.Workspace("ws"), now,
		domtypes.None[time.Time](), "active", 1, 1, nil, "", "", domtypes.SessionID(""),
	)

	legacy, items, err := builder.loadRecentCommands(context.Background(), session, 1)
	if err != nil {
		t.Fatalf("loadRecentCommands() error = %v", err)
	}
	if previewQuery.requestedRunes != contextPackCommandPreviewRuneLimit {
		t.Fatalf("body rune limit = %d, want %d", previewQuery.requestedRunes, contextPackCommandPreviewRuneLimit)
	}
	if len(legacy) != 1 || legacy[0] != "go test ./..." {
		t.Fatalf("legacy = %#v, want normalized summary", legacy)
	}
	if len(items) != 1 || items[0].Summary() != legacy[0] {
		t.Fatalf("items = %#v, want matching structured summary", items)
	}
	item := items[0]
	if item.EventID() != domtypes.EventID("event-command") || !item.ResponseTruncated() {
		t.Fatalf("item identity/truncation = %s/%t", item.EventID(), item.ResponseTruncated())
	}
	if item.BodyExtent().StoredBytes() != 10_000 || item.ReturnedBytes() != len("go test ./...") {
		t.Fatalf("item extent = stored %d returned %d", item.BodyExtent().StoredBytes(), item.ReturnedBytes())
	}
}
