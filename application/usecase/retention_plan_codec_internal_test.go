package usecase

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/xerrors"
)

func TestCanonicalizeJSON_matchesCheckedInGoldenVector(t *testing.T) {
	t.Parallel()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	canonical, err := os.ReadFile(filepath.Join(root, "docs", "operations", "testdata", "retention-plan-canonical.golden.json"))
	if err != nil {
		t.Fatalf("read canonical fixture: %v", err)
	}
	plan, err := os.ReadFile(filepath.Join(root, "docs", "operations", "testdata", "retention-plan.golden.json"))
	if err != nil {
		t.Fatalf("read plan fixture: %v", err)
	}
	decoded, err := decodeGoldenPayload(plan)
	if err != nil {
		t.Fatalf("decode golden payload: %v", err)
	}
	got, err := canonicalizeJSON(decoded)
	if err != nil {
		t.Fatalf("canonicalizeJSON() error = %v", err)
	}
	if string(got) != string(canonical) {
		t.Fatal("canonical bytes do not match checked-in golden vector")
	}
	digest := sha256.Sum256(got)
	if hex.EncodeToString(digest[:]) != "3b101f3dcfeb284816dd34fc8de8c1f28df96181f505a385bbdbd0ff9cfdbba8" {
		t.Fatalf("canonical digest = %s", hex.EncodeToString(digest[:]))
	}
}

func TestRawBodyCandidateIdentity_roundTripsOpaqueEventID(t *testing.T) {
	t.Parallel()

	const (
		eventID = "event:with/slash:日本語"
		digest  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	encoded := rawBodyCandidateIdentity(eventID, digest)
	gotID, gotDigest, err := parseRawBodyCandidateIdentity(encoded)
	if err != nil {
		t.Fatalf("parseRawBodyCandidateIdentity() error = %v", err)
	}
	if gotID != eventID || gotDigest != digest {
		t.Fatalf("round trip = (%q, %q)", gotID, gotDigest)
	}
}

func decodeGoldenPayload(plan []byte) ([]byte, error) {
	var envelope struct {
		CanonicalPayload any `json:"canonical_payload"`
	}
	if err := json.Unmarshal(plan, &envelope); err != nil {
		return nil, xerrors.Errorf("unmarshal golden plan: %w", err)
	}
	encoded, err := json.Marshal(envelope.CanonicalPayload)
	if err != nil {
		return nil, xerrors.Errorf("marshal golden payload: %w", err)
	}
	return encoded, nil
}
