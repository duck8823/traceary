package usecase

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestMemoryHygieneCursor_RoundTrip(t *testing.T) {
	t.Parallel()

	payload := memoryHygieneCursorPayload{
		Version:        memoryHygieneCursorVersion,
		Revision:       42,
		CriteriaDigest: strings.Repeat("a", 64),
		ScanAt:         time.Date(2026, 7, 26, 8, 0, 0, 123, time.UTC).Format(time.RFC3339Nano),
		Phase:          apptypes.MemoryHygieneScanPhaseSimilarityPairs,
		Keyset: apptypes.MemoryHygieneScanKeyset{
			AfterMemoryID:  "mem-a",
			AnchorMemoryID: "mem-b",
			AfterPartnerID: "mem-c",
		},
	}
	cursor, err := encodeMemoryHygieneCursor(payload)
	if err != nil {
		t.Fatalf("encodeMemoryHygieneCursor() error = %v", err)
	}
	if strings.Contains(cursor, "memory fact") {
		t.Fatalf("cursor leaked fact text: %q", cursor)
	}
	rawCursor, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatalf("DecodeString(cursor) error = %v", err)
	}
	if strings.Contains(string(rawCursor), "fact") {
		t.Fatalf("decoded cursor contains a fact field: %s", rawCursor)
	}
	got, err := decodeMemoryHygieneCursor(cursor)
	if err != nil {
		t.Fatalf("decodeMemoryHygieneCursor() error = %v", err)
	}
	if got != payload {
		t.Fatalf("decoded payload = %#v, want %#v", got, payload)
	}
}

func TestMemoryHygieneCursor_RejectsTamperingWithoutEchoingInput(t *testing.T) {
	t.Parallel()

	cursor, err := encodeMemoryHygieneCursor(memoryHygieneCursorPayload{
		Revision:       9,
		CriteriaDigest: strings.Repeat("b", 64),
		ScanAt:         "2026-07-26T00:00:00Z",
		Phase:          apptypes.MemoryHygieneScanPhaseAcceptedRows,
	})
	if err != nil {
		t.Fatalf("encodeMemoryHygieneCursor() error = %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	var envelope memoryHygieneCursorEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	envelope.Payload.Revision++
	tamperedRaw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	tampered := base64.RawURLEncoding.EncodeToString(tamperedRaw)
	_, err = decodeMemoryHygieneCursor(tampered)
	if err == nil {
		t.Fatal("decodeMemoryHygieneCursor() error = nil, want checksum error")
	}
	if strings.Contains(err.Error(), tampered) {
		t.Fatalf("error echoed opaque cursor: %v", err)
	}
}

func TestMemoryHygieneCursor_RejectsUnknownPhaseAndVersion(t *testing.T) {
	t.Parallel()

	tests := map[string]memoryHygieneCursorEnvelope{
		"version": {
			Payload: memoryHygieneCursorPayload{
				Version:        2,
				Revision:       1,
				CriteriaDigest: strings.Repeat("c", 64),
				ScanAt:         "2026-07-26T00:00:00Z",
				Phase:          apptypes.MemoryHygieneScanPhaseAcceptedRows,
			},
		},
		"phase": {
			Payload: memoryHygieneCursorPayload{
				Version:        memoryHygieneCursorVersion,
				Revision:       1,
				CriteriaDigest: strings.Repeat("d", 64),
				ScanAt:         "2026-07-26T00:00:00Z",
				Phase:          apptypes.MemoryHygieneScanPhase("unknown"),
			},
		},
	}
	for name, envelope := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checksum, err := memoryHygieneCursorChecksum(envelope.Payload)
			if err != nil {
				t.Fatalf("memoryHygieneCursorChecksum() error = %v", err)
			}
			envelope.Checksum = checksum
			raw, err := json.Marshal(envelope)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			_, err = decodeMemoryHygieneCursor(base64.RawURLEncoding.EncodeToString(raw))
			if err == nil {
				t.Fatal("decodeMemoryHygieneCursor() error = nil, want validation error")
			}
		})
	}
}

func TestMemoryHygieneCursor_RejectsInconsistentKeyset(t *testing.T) {
	t.Parallel()

	payload := memoryHygieneCursorPayload{
		Version:        memoryHygieneCursorVersion,
		Revision:       1,
		CriteriaDigest: strings.Repeat("e", 64),
		ScanAt:         "2026-07-26T00:00:00Z",
		Phase:          apptypes.MemoryHygieneScanPhaseSimilarityPairs,
		Keyset: apptypes.MemoryHygieneScanKeyset{
			AfterMemoryID:  "mem-b",
			AnchorMemoryID: "mem-a",
			AfterPartnerID: "mem-c",
		},
	}
	checksum, err := memoryHygieneCursorChecksum(payload)
	if err != nil {
		t.Fatalf("memoryHygieneCursorChecksum() error = %v", err)
	}
	raw, err := json.Marshal(memoryHygieneCursorEnvelope{Payload: payload, Checksum: checksum})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if _, err := decodeMemoryHygieneCursor(base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("decodeMemoryHygieneCursor() error = nil, want inconsistent-keyset error")
	}
}

func TestMemoryHygieneCriteriaDigest_IsOrderIndependentAndCriteriaBound(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceFrom("github.com/duck8823/traceary")
	if err != nil {
		t.Fatalf("WorkspaceFrom() error = %v", err)
	}
	agent, err := domtypes.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	workspaceScope := domtypes.WorkspaceScopeOf(workspace)
	agentScope := domtypes.AgentScopeOf(agent)

	first := memoryHygieneCriteriaDigest(
		[]domtypes.MemoryScope{workspaceScope, agentScope},
		90*24*time.Hour,
		0.6,
		false,
	)
	reordered := memoryHygieneCriteriaDigest(
		[]domtypes.MemoryScope{agentScope, workspaceScope},
		90*24*time.Hour,
		0.6,
		false,
	)
	if first != reordered {
		t.Fatalf("criteria digest depends on scope order: %q != %q", first, reordered)
	}
	changed := memoryHygieneCriteriaDigest(
		[]domtypes.MemoryScope{workspaceScope, agentScope},
		30*24*time.Hour,
		0.6,
		false,
	)
	if first == changed {
		t.Fatal("criteria digest did not bind staleness threshold")
	}
}
