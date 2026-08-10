package usecase

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
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
	if hex.EncodeToString(digest[:]) != "519b1e4039fcf9afb33619ae18931d0f7344649b22b04b9fc9716e672722b69e" {
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

func TestDecodeRetentionPlanRejectsV1WithVersionInFailure(t *testing.T) {
	t.Parallel()

	planData, err := os.ReadFile(filepath.Join("..", "..", "docs", "operations", "testdata", "retention-plan.golden.json"))
	if err != nil {
		t.Fatalf("read golden plan: %v", err)
	}
	var plan apptypes.RetentionPlan
	if err := json.Unmarshal(planData, &plan); err != nil {
		t.Fatalf("unmarshal golden plan: %v", err)
	}
	plan.CanonicalPayload.SchemaVersion = "retention-plan/v1"
	canonical, err := canonicalRetentionPayload(plan.CanonicalPayload)
	if err != nil {
		t.Fatalf("canonicalize v1 payload: %v", err)
	}
	digest := sha256.Sum256(canonical)
	plan.PlanID = hex.EncodeToString(digest[:])
	planData, err = json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal v1 plan: %v", err)
	}
	_, err = decodeRetentionPlan(planData)
	if err == nil || !strings.Contains(err.Error(), `unsupported retention plan schema "retention-plan/v1"`) {
		t.Fatalf("decodeRetentionPlan() error = %v, want named v1 version failure", err)
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
