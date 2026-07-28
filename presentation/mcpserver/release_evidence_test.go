package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

type v0330AggregateReleaseEvidence struct {
	MaxItems                      int  `json:"max_items"`
	MaxAggregateBodyBytes         int  `json:"max_aggregate_body_bytes"`
	ObservedMaxItems              int  `json:"observed_max_items"`
	ObservedMaxAggregateBodyBytes int  `json:"observed_max_aggregate_body_bytes"`
	Pages                         int  `json:"pages"`
	TotalItems                    int  `json:"total_items"`
	MultibyteObserved             bool `json:"multibyte_observed"`
	BodyBlocksObserved            bool `json:"body_blocks_observed"`
	TruncationMetadataObserved    bool `json:"truncation_metadata_observed"`
	ContinuationNoDuplicateOrSkip bool `json:"continuation_no_duplicate_or_skip"`
}

// TestV0330AggregateReleaseEvidence exercises the actual MCP list page path
// with 100 multibyte canonical events. It emits only numeric and boolean
// evidence; bodies, IDs, body blocks, and opaque continuations stay in-memory.
func TestV0330AggregateReleaseEvidence(t *testing.T) {
	server, datasource := newEventContinuationIntegrationServer(t)
	createdAt := time.Date(2026, 7, 26, 6, 0, 0, 0, time.UTC)
	visible := strings.Repeat("日本語", 100)
	envelopeBytes, err := json.Marshal(map[string]any{
		"blocks": []map[string]string{
			{"type": "text", "text": visible},
			{"type": "thinking", "text": "synthetic private thinking"},
			{"type": "text", "text": "bounded release evidence"},
		},
	})
	if err != nil {
		t.Fatalf("marshal canonical fixture: %v", err)
	}
	expected := make(map[string]struct{}, apptypes.MaxEventResponseItemLimit)
	for index := 0; index < apptypes.MaxEventResponseItemLimit; index++ {
		eventID := fmt.Sprintf("release-event-%03d", index)
		expected[eventID] = struct{}{}
		body := string(envelopeBytes)
		if index == 0 {
			body = strings.Repeat("長", 700)
		}
		saveEventContinuationFixture(t, datasource, eventID, body, createdAt)
	}

	bodyLimit := 500
	continuation := ""
	seen := make(map[string]struct{}, len(expected))
	evidence := v0330AggregateReleaseEvidence{
		MaxItems:              apptypes.MaxEventResponseItemLimit,
		MaxAggregateBodyBytes: apptypes.MaxEventResponseAggregateBodyBytes,
	}
	for {
		_, page, err := server.listEvents()(
			context.Background(),
			nil,
			listEventsInput{
				Workspace:    "repo",
				Projection:   "bounded",
				Limit:        apptypes.MaxEventResponseItemLimit,
				BodyLimit:    &bodyLimit,
				Continuation: continuation,
			},
		)
		if err != nil {
			t.Fatalf("listEvents() page error = %v", err)
		}
		evidence.Pages++
		if len(page.Events) > evidence.ObservedMaxItems {
			evidence.ObservedMaxItems = len(page.Events)
		}
		if page.Coverage.AggregateBodyBytes > evidence.ObservedMaxAggregateBodyBytes {
			evidence.ObservedMaxAggregateBodyBytes = page.Coverage.AggregateBodyBytes
		}
		if len(page.Events) > apptypes.MaxEventResponseItemLimit {
			t.Fatalf("page items = %d, want <= %d", len(page.Events), apptypes.MaxEventResponseItemLimit)
		}
		if page.Coverage.AggregateBodyBytes > apptypes.MaxEventResponseAggregateBodyBytes {
			t.Fatalf(
				"page aggregate body bytes = %d, want <= %d",
				page.Coverage.AggregateBodyBytes,
				apptypes.MaxEventResponseAggregateBodyBytes,
			)
		}
		for _, event := range page.Events {
			if _, duplicate := seen[event.EventID]; duplicate {
				t.Fatal("aggregate continuation returned a duplicate item")
			}
			if _, known := expected[event.EventID]; !known {
				t.Fatal("aggregate continuation returned an unexpected item")
			}
			seen[event.EventID] = struct{}{}
			if event.Body != nil && strings.Contains(*event.Body, "日本語") {
				evidence.MultibyteObserved = true
			}
			if len(event.BodyBlocks) > 0 {
				evidence.BodyBlocksObserved = true
			}
			if event.BodyTruncated &&
				event.BodyLength > 0 &&
				event.Body != nil &&
				len([]rune(*event.Body)) < event.BodyLength {
				evidence.TruncationMetadataObserved = true
			}
		}
		if page.Continuation == "" {
			break
		}
		continuation = page.Continuation
	}
	for eventID := range expected {
		if _, ok := seen[eventID]; !ok {
			t.Fatal("aggregate continuation skipped an item")
		}
	}
	evidence.TotalItems = len(seen)
	evidence.ContinuationNoDuplicateOrSkip = len(seen) == len(expected)
	if evidence.Pages < 2 ||
		!evidence.MultibyteObserved ||
		!evidence.BodyBlocksObserved ||
		!evidence.TruncationMetadataObserved ||
		!evidence.ContinuationNoDuplicateOrSkip {
		t.Fatalf("aggregate release evidence invariants failed: %+v", evidence)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("marshal aggregate release evidence: %v", err)
	}
	t.Logf("TRACEARY_PHASE_D_EVIDENCE=%s", encoded)
}
