package usecase

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
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
		Consistency: apptypes.MemoryHygieneScanConsistencyConsistent,
	}
	cursor, err := encodeMemoryHygieneCursor(payload)
	if err != nil {
		t.Fatalf("encodeMemoryHygieneCursor() error = %v", err)
	}
	if strings.Contains(cursor, "mem-b") || strings.Contains(cursor, payload.CriteriaDigest) {
		t.Fatalf("cursor leaked plaintext fields: %q", cursor)
	}
	rawCursor, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatalf("DecodeString(cursor) error = %v", err)
	}
	if strings.Contains(string(rawCursor), "mem-b") || strings.Contains(string(rawCursor), payload.CriteriaDigest) {
		t.Fatal("decoded cursor exposed plaintext fields")
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
		Consistency:    apptypes.MemoryHygieneScanConsistencyConsistent,
	})
	if err != nil {
		t.Fatalf("encodeMemoryHygieneCursor() error = %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	raw[len(raw)-1] ^= 0x01
	tampered := base64.RawURLEncoding.EncodeToString(raw)
	_, err = decodeMemoryHygieneCursor(tampered)
	if !errors.Is(err, queryservice.ErrMemoryHygieneRescanRequired) {
		t.Fatalf("decodeMemoryHygieneCursor() error = %v, want rescan required", err)
	}
	if strings.Contains(err.Error(), tampered) {
		t.Fatalf("error echoed opaque cursor: %v", err)
	}
	if !strings.Contains(err.Error(), "start a new scan") {
		t.Fatalf("authentication error has no rescan guidance: %v", err)
	}
}

func TestMemoryHygieneCursor_RejectsEarlierProcessAndLegacyChecksumCursor(t *testing.T) {
	t.Parallel()

	foreignAEAD := newMemoryHygieneTestAEAD(t, 1)
	foreign, err := encodeMemoryHygieneCursorWithAEAD(memoryHygieneCursorPayload{
		Version:        memoryHygieneCursorVersion,
		Revision:       1,
		CriteriaDigest: strings.Repeat("c", 64),
		ScanAt:         "2026-07-26T00:00:00Z",
		Phase:          apptypes.MemoryHygieneScanPhaseAcceptedRows,
		Consistency:    apptypes.MemoryHygieneScanConsistencyConsistent,
	}, foreignAEAD)
	if err != nil {
		t.Fatalf("encodeMemoryHygieneCursorWithAEAD() error = %v", err)
	}
	if _, err := decodeMemoryHygieneCursor(foreign); !errors.Is(err, queryservice.ErrMemoryHygieneRescanRequired) ||
		!strings.Contains(err.Error(), "process restarted") {
		t.Fatalf("earlier-process cursor error = %v, want restart rescan guidance", err)
	}

	legacyRaw, err := json.Marshal(struct {
		Payload  map[string]any `json:"payload"`
		Checksum string         `json:"checksum"`
	}{
		Payload: map[string]any{
			"v": 1, "r": 1, "c": strings.Repeat("d", 64), "at": "2026-07-26T00:00:00Z",
			"p": "accepted_rows", "k": map[string]any{},
		},
		Checksum: strings.Repeat("0", 32),
	})
	if err != nil {
		t.Fatalf("json.Marshal(legacy cursor) error = %v", err)
	}
	legacy := base64.RawURLEncoding.EncodeToString(legacyRaw)
	if _, err := decodeMemoryHygieneCursor(legacy); !errors.Is(err, queryservice.ErrMemoryHygieneRescanRequired) {
		t.Fatalf("legacy cursor error = %v, want rescan required", err)
	}
}

func TestMemoryHygieneCursor_RejectsUnknownPhaseAndVersion(t *testing.T) {
	t.Parallel()

	aead, err := loadMemoryHygieneCursorAEAD()
	if err != nil {
		t.Fatalf("loadMemoryHygieneCursorAEAD() error = %v", err)
	}
	tests := map[string]memoryHygieneCursorPayload{
		"version": {
			Version:        2,
			Revision:       1,
			CriteriaDigest: strings.Repeat("c", 64),
			ScanAt:         "2026-07-26T00:00:00Z",
			Phase:          apptypes.MemoryHygieneScanPhaseAcceptedRows,
			Consistency:    apptypes.MemoryHygieneScanConsistencyConsistent,
		},
		"phase": {
			Version:        memoryHygieneCursorVersion,
			Revision:       1,
			CriteriaDigest: strings.Repeat("d", 64),
			ScanAt:         "2026-07-26T00:00:00Z",
			Phase:          apptypes.MemoryHygieneScanPhase("unknown"),
			Consistency:    apptypes.MemoryHygieneScanConsistencyConsistent,
		},
	}
	tests["version"] = func(payload memoryHygieneCursorPayload) memoryHygieneCursorPayload {
		payload.Version = memoryHygieneCursorVersion + 1
		return payload
	}(tests["version"])
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			encoded, err := encodeMemoryHygieneCursorWithAEAD(payload, aead)
			if err != nil {
				t.Fatalf("encodeMemoryHygieneCursorWithAEAD() error = %v", err)
			}
			_, err = decodeMemoryHygieneCursor(encoded)
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
		Consistency: apptypes.MemoryHygieneScanConsistencyConsistent,
	}
	aead, err := loadMemoryHygieneCursorAEAD()
	if err != nil {
		t.Fatalf("loadMemoryHygieneCursorAEAD() error = %v", err)
	}
	encoded, err := encodeMemoryHygieneCursorWithAEAD(payload, aead)
	if err != nil {
		t.Fatalf("encodeMemoryHygieneCursorWithAEAD() error = %v", err)
	}
	if _, err := decodeMemoryHygieneCursor(encoded); err == nil {
		t.Fatal("decodeMemoryHygieneCursor() error = nil, want inconsistent-keyset error")
	}
}

func TestMemoryHygieneCursor_RejectsInconsistentConsistencyState(t *testing.T) {
	t.Parallel()

	aead, err := loadMemoryHygieneCursorAEAD()
	if err != nil {
		t.Fatalf("loadMemoryHygieneCursorAEAD() error = %v", err)
	}
	tests := map[string]memoryHygieneCursorPayload{
		"consistent with reason": {
			Version: memoryHygieneCursorVersion, Revision: 1, CriteriaDigest: strings.Repeat("f", 64),
			ScanAt: "2026-07-26T00:00:00Z", Phase: apptypes.MemoryHygieneScanPhaseAcceptedRows,
			Consistency:       apptypes.MemoryHygieneScanConsistencyConsistent,
			ConsistencyReason: apptypes.MemoryHygieneConsistencyReasonRevisionChanged,
		},
		"best effort without reason": {
			Version: memoryHygieneCursorVersion, Revision: 1, CriteriaDigest: strings.Repeat("f", 64),
			ScanAt: "2026-07-26T00:00:00Z", Phase: apptypes.MemoryHygieneScanPhaseAcceptedRows,
			Consistency: apptypes.MemoryHygieneScanConsistencyBestEffort,
		},
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			encoded, encodeErr := encodeMemoryHygieneCursorWithAEAD(payload, aead)
			if encodeErr != nil {
				t.Fatalf("encodeMemoryHygieneCursorWithAEAD() error = %v", encodeErr)
			}
			if _, decodeErr := decodeMemoryHygieneCursor(encoded); decodeErr == nil {
				t.Fatal("decodeMemoryHygieneCursor() error = nil, want consistency validation error")
			}
		})
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

func newMemoryHygieneTestAEAD(t *testing.T, firstByte byte) cipher.AEAD {
	t.Helper()
	key := make([]byte, 32)
	key[0] = firstByte
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher() error = %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM() error = %v", err)
	}
	return aead
}
