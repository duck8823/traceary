package types_test

import (
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func TestBoundedEventOf_PreservesSeparateResponseAndPersistenceTruncation(t *testing.T) {
	t.Parallel()

	metadata := boundedEventMetadataFixture(t, types.Some(true), types.Some(false))
	event, err := apptypes.BoundedEventOf(
		metadata,
		"visible",
		7,
		20,
		true,
	)
	if err != nil {
		t.Fatalf("BoundedEventOf() error = %v", err)
	}
	if !event.BodyResponseTruncated() {
		t.Fatal("BodyResponseTruncated() = false, want true")
	}
	if got := event.VisibleBodyRunes(); got != 20 {
		t.Fatalf("VisibleBodyRunes() = %d, want 20", got)
	}
	ingest, ok := event.Metadata().BodyExtent().IngestTruncated().Value()
	if !ok || !ingest {
		t.Fatalf("persisted ingest truncation = (%t, %t), want (true, true)", ingest, ok)
	}
	storage, ok := event.Metadata().BodyExtent().StorageTruncated().Value()
	if !ok || storage {
		t.Fatalf("persisted storage truncation = (%t, %t), want (false, true)", storage, ok)
	}
}

func TestBoundedEventOf_RejectsImpossibleBodyProjection(t *testing.T) {
	t.Parallel()

	metadata := boundedEventMetadataFixture(t, types.None[bool](), types.None[bool]())
	tests := []struct {
		name         string
		body         string
		visibleRunes int
		canonical    bool
	}{
		{
			name:         "prefix exceeds visible length",
			body:         "too long",
			visibleRunes: 2,
		},
		{
			name:         "negative visible length",
			visibleRunes: -1,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := apptypes.BoundedEventOf(
				metadata,
				tt.body,
				max(1, len([]rune(tt.body))),
				tt.visibleRunes,
				tt.canonical,
			); err == nil {
				t.Fatal("BoundedEventOf() error = nil")
			}
		})
	}
}

func TestBoundedEvent_WithCanonicalBodyBlocksRequiresUntruncatedCanonicalBody(t *testing.T) {
	t.Parallel()

	metadata := boundedEventMetadataFixture(t, types.None[bool](), types.None[bool]())
	blocks := []apptypes.EventBodyBlock{{Type: apptypes.EventBodyBlockTypeText, Text: "visible"}}

	canonical, err := apptypes.BoundedEventOf(
		metadata,
		"visible",
		7,
		7,
		true,
	)
	if err != nil {
		t.Fatalf("BoundedEventOf(canonical) error = %v", err)
	}
	withBlocks, err := canonical.WithCanonicalBodyBlocks(blocks)
	if err != nil {
		t.Fatalf("WithCanonicalBodyBlocks() error = %v", err)
	}
	if len(withBlocks.BodyBlocks()) != 1 || withBlocks.BodyBlocks()[0].Text != "visible" {
		t.Fatalf("BodyBlocks() = %+v", withBlocks.BodyBlocks())
	}

	truncated, err := apptypes.BoundedEventOf(
		metadata,
		"vis",
		3,
		7,
		true,
	)
	if err != nil {
		t.Fatalf("BoundedEventOf(truncated) error = %v", err)
	}
	if _, err := truncated.WithCanonicalBodyBlocks(blocks); err == nil {
		t.Fatal("WithCanonicalBodyBlocks(truncated) error = nil")
	}

	plain, err := apptypes.BoundedEventOf(
		metadata,
		"visible",
		7,
		7,
		false,
	)
	if err != nil {
		t.Fatalf("BoundedEventOf(plain) error = %v", err)
	}
	if _, err := plain.WithCanonicalBodyBlocks(blocks); err == nil {
		t.Fatal("WithCanonicalBodyBlocks(noncanonical) error = nil")
	}
}

func boundedEventMetadataFixture(
	t *testing.T,
	ingestTruncated types.Optional[bool],
	storageTruncated types.Optional[bool],
) apptypes.EventMetadata {
	t.Helper()
	extent, err := apptypes.EventBodyExtentOf(
		types.Some(4096),
		2048,
		ingestTruncated,
		storageTruncated,
		types.Some(1),
	)
	if err != nil {
		t.Fatalf("EventBodyExtentOf() error = %v", err)
	}
	metadata, err := apptypes.EventMetadataOf(
		types.EventID("event-bounded"),
		types.EventKindTranscript,
		types.Client("hook"),
		types.Agent("codex"),
		types.SessionID("session-1"),
		types.Workspace("duck8823/traceary"),
		"stop",
		time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC),
		extent,
		types.None[apptypes.CommandAuditMetadata](),
	)
	if err != nil {
		t.Fatalf("EventMetadataOf() error = %v", err)
	}
	if strings.TrimSpace(metadata.EventID().String()) == "" {
		t.Fatal("metadata fixture has empty event ID")
	}
	return metadata
}
