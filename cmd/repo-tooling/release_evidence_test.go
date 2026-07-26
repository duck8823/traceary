package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateBodyFreeEvidence_AcceptsCompleteMetricsOnlyArtifact(t *testing.T) {
	t.Parallel()

	evidence := validBodyFreeEvidenceFixture()
	if err := validateBodyFreeEvidence(evidence); err != nil {
		t.Fatalf("validateBodyFreeEvidence() error = %v", err)
	}

	var encoded bytes.Buffer
	if err := writeBodyFreeEvidence(&encoded, "", evidence); err != nil {
		t.Fatalf("writeBodyFreeEvidence() error = %v", err)
	}
	for _, forbidden := range []string{
		`"event_id"`, `"session_id"`, `"workspace"`, `"path"`,
		`"cursor"`, `"continuation"`, `"prompt"`, `"response"`, `"body"`,
	} {
		if strings.Contains(encoded.String(), forbidden) {
			t.Fatalf("evidence contains forbidden key %s:\n%s", forbidden, encoded.String())
		}
	}
	if _, err := decodeBodyFreeEvidence(bytes.NewReader(encoded.Bytes())); err != nil {
		t.Fatalf("decodeBodyFreeEvidence() error = %v", err)
	}
}

func TestDecodeBodyFreeEvidence_RejectsUnknownSensitiveField(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(validBodyFreeEvidenceFixture())
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	encoded = bytes.TrimSuffix(encoded, []byte("}"))
	encoded = append(encoded, []byte(`,"path":"/private/tmp/private-store"}`)...)
	if _, err := decodeBodyFreeEvidence(bytes.NewReader(encoded)); err == nil {
		t.Fatal("decodeBodyFreeEvidence() error = nil, want a sensitive-field rejection")
	}
}

func TestValidateBodyFreeEvidence_RejectsArbitraryStringFields(t *testing.T) {
	t.Parallel()

	evidence := validBodyFreeEvidenceFixture()
	evidence.Preflight.Reason = "/private/tmp/private-store"
	if err := validateBodyFreeEvidence(evidence); err == nil {
		t.Fatal("arbitrary preflight reason unexpectedly passed")
	}

	evidence = validBodyFreeEvidenceFixture()
	evidence.Host.GoVersion = "private-secret"
	if err := validateBodyFreeEvidence(evidence); err == nil {
		t.Fatal("arbitrary host version unexpectedly passed")
	}
}

func TestExtractEvidenceMarker_RejectsTrailingData(t *testing.T) {
	t.Parallel()

	valid := validBodyFreeEvidenceFixture()
	marker := markerFixture(t, bodyFreeEvidencePhaseDMarker, *valid.PhaseD)
	marker = bytes.Replace(marker, []byte("}\n"), []byte("} unexpected\n"), 1)
	var phase bodyFreeEvidencePhaseD
	if err := extractEvidenceMarker(marker, bodyFreeEvidencePhaseDMarker, &phase); err == nil {
		t.Fatal("marker with trailing data unexpectedly passed")
	}
}

func TestValidateBodyFreeEvidence_RejectsMetadataBodyBytesAndPhaseCGatesNoLatency(t *testing.T) {
	t.Parallel()

	evidence := validBodyFreeEvidenceFixture()
	for index := range evidence.PhaseC {
		if evidence.PhaseC[index].Projection == "metadata" {
			evidence.PhaseC[index].ReturnedBodyBytes = 1
			break
		}
	}
	if err := validateBodyFreeEvidence(evidence); err == nil {
		t.Fatal("metadata body bytes unexpectedly passed")
	}

	evidence = validBodyFreeEvidenceFixture()
	for index := range evidence.PhaseC {
		evidence.PhaseC[index].P95MS = 999_999
	}
	if err := validateBodyFreeEvidence(evidence); err != nil {
		t.Fatalf("Phase-C observation-only p95 unexpectedly failed: %v", err)
	}
}

func TestValidateBodyFreeEvidence_RequiresOrderedIndexButOnlyObservesCoveringIndex(t *testing.T) {
	t.Parallel()

	evidence := validBodyFreeEvidenceFixture()
	evidence.PhaseA.CoveringIndex = false
	if err := validateBodyFreeEvidence(evidence); err != nil {
		t.Fatalf("non-covering Phase-A plan unexpectedly failed validation: %v", err)
	}

	evidence.PhaseA.OrderedIndex = false
	if err := validateBodyFreeEvidence(evidence); err == nil {
		t.Fatal("Phase-A plan without its ordered index unexpectedly passed")
	}
}

func TestValidateBodyFreeEvidence_ValidatesPresentPhasesInBlockedArtifact(t *testing.T) {
	t.Parallel()

	evidence := validBodyFreeEvidenceFixture()
	evidence.Status = "blocked"
	evidence.BlockReason = "phase_a_failed"
	evidence.PhaseA.P95MS = 300
	evidence.PhaseA.Passed = false
	if err := validateBodyFreeEvidence(evidence); err != nil {
		t.Fatalf("valid blocked evidence unexpectedly failed: %v", err)
	}

	evidence.PhaseB.Events = 0
	if err := validateBodyFreeEvidence(evidence); err == nil {
		t.Fatal("blocked evidence with an invalid present phase unexpectedly passed")
	}
}

func TestCollectV0330BodyFreeEvidence_BlocksBeforeScratchWhenDiskIsInsufficient(t *testing.T) {
	t.Parallel()

	phaseCalled := false
	deps := defaultReleaseEvidenceDependencies()
	deps.availableScratchBytes = func(string) (uint64, error) {
		return v0330EvidenceRequiredScratchBytes - 1, nil
	}
	deps.runPhase = func(context.Context, string, string, releaseEvidencePhase) ([]byte, error) {
		phaseCalled = true
		return nil, errors.New("must not run")
	}
	evidence := collectV0330BodyFreeEvidence(context.Background(), t.TempDir(), t.TempDir(), deps)
	if evidence.Status != "blocked" || evidence.BlockReason != "insufficient_disk" {
		t.Fatalf("blocked evidence = %+v", evidence)
	}
	if evidence.Preflight.Capable || !evidence.Privacy.MetricsOnly || !evidence.Privacy.ScratchCleaned {
		t.Fatalf("blocked preflight/privacy = %+v / %+v", evidence.Preflight, evidence.Privacy)
	}
	if phaseCalled {
		t.Fatal("a multi-GiB phase ran after insufficient-disk preflight")
	}
	if err := validateBodyFreeEvidence(evidence); err != nil {
		t.Fatalf("sanitized blocked evidence is invalid: %v", err)
	}
}

func TestCollectV0330BodyFreeEvidence_CleansPrivateScratchAndMergesMarkers(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	var observedScratch string
	deps := defaultReleaseEvidenceDependencies()
	deps.availableScratchBytes = func(string) (uint64, error) {
		return v0330EvidenceRequiredScratchBytes, nil
	}
	deps.verifyHosts = func(string) error { return nil }
	deps.runPhase = func(_ context.Context, _ string, scratch string, phase releaseEvidencePhase) ([]byte, error) {
		observedScratch = scratch
		info, err := os.Stat(scratch)
		if err != nil {
			t.Fatalf("Stat(scratch) error = %v", err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("scratch mode = %o, want 0700", info.Mode().Perm())
		}
		switch phase {
		case releaseEvidencePhaseA:
			return markerFixture(t, bodyFreeEvidencePhaseAMarker, *validBodyFreeEvidenceFixture().PhaseA), nil
		case releaseEvidencePhaseBC:
			valid := validBodyFreeEvidenceFixture()
			return markerFixture(t, bodyFreeEvidencePhaseBCMarker, bodyFreeEvidencePhaseBC{
				PhaseB: *valid.PhaseB,
				PhaseC: valid.PhaseC,
			}), nil
		case releaseEvidencePhaseD:
			return markerFixture(t, bodyFreeEvidencePhaseDMarker, *validBodyFreeEvidenceFixture().PhaseD), nil
		default:
			return nil, errors.New("unsupported phase")
		}
	}

	evidence := collectV0330BodyFreeEvidence(context.Background(), t.TempDir(), parent, deps)
	if evidence.Status != "pass" {
		t.Fatalf("collectV0330BodyFreeEvidence() = %+v", evidence)
	}
	if observedScratch == "" {
		t.Fatal("no scratch directory was observed")
	}
	if _, err := os.Stat(observedScratch); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch still exists or returned an unexpected error: %v", err)
	}
	if err := validateBodyFreeEvidence(evidence); err != nil {
		t.Fatalf("validateBodyFreeEvidence() error = %v", err)
	}

	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), filepath.ToSlash(observedScratch)) {
		t.Fatalf("evidence exposed scratch path: %s", encoded)
	}
}

func TestCollectV0330BodyFreeEvidence_SanitizesInvalidPhaseMetrics(t *testing.T) {
	t.Parallel()

	deps := defaultReleaseEvidenceDependencies()
	deps.availableScratchBytes = func(string) (uint64, error) {
		return v0330EvidenceRequiredScratchBytes, nil
	}
	deps.verifyHosts = func(string) error { return nil }
	deps.runPhase = func(_ context.Context, _ string, _ string, phase releaseEvidencePhase) ([]byte, error) {
		valid := validBodyFreeEvidenceFixture()
		switch phase {
		case releaseEvidencePhaseA:
			valid.PhaseA.P95MS = -1
			return markerFixture(t, bodyFreeEvidencePhaseAMarker, *valid.PhaseA), nil
		case releaseEvidencePhaseBC:
			return markerFixture(t, bodyFreeEvidencePhaseBCMarker, bodyFreeEvidencePhaseBC{
				PhaseB: *valid.PhaseB,
				PhaseC: valid.PhaseC,
			}), nil
		case releaseEvidencePhaseD:
			return markerFixture(t, bodyFreeEvidencePhaseDMarker, *valid.PhaseD), nil
		default:
			return nil, errors.New("unsupported phase")
		}
	}

	evidence := collectV0330BodyFreeEvidence(context.Background(), t.TempDir(), t.TempDir(), deps)
	if evidence.Status != "blocked" || evidence.BlockReason != "evidence_validation_failed" {
		t.Fatalf("invalid phase evidence = %+v", evidence)
	}
	if evidence.PhaseA != nil || evidence.PhaseB != nil || len(evidence.PhaseC) != 0 ||
		evidence.PhaseD != nil || evidence.PhaseE != nil {
		t.Fatalf("invalid phase metrics were retained: %+v", evidence)
	}
	if err := validateBodyFreeEvidence(evidence); err != nil {
		t.Fatalf("sanitized blocker is invalid: %v", err)
	}
}

func markerFixture(t *testing.T, marker string, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(marker) error = %v", err)
	}
	return append(append([]byte("test output\n"+marker), encoded...), '\n')
}

func validBodyFreeEvidenceFixture() BodyFreeEvidence {
	probes := make([]bodyFreeEvidenceProbe, 0, 8)
	for _, row := range []struct {
		operation, projection, fts string
	}{
		{"list", "metadata", "not_applicable"},
		{"list", "bounded", "not_applicable"},
		{"context", "metadata", "not_applicable"},
		{"context", "bounded", "not_applicable"},
		{"search", "metadata", "incomplete"},
		{"search", "bounded", "incomplete"},
		{"search", "metadata", "complete"},
		{"search", "bounded", "complete"},
	} {
		bodyBytes := 0
		if row.projection == "bounded" {
			bodyBytes = 512
		}
		probes = append(probes, bodyFreeEvidenceProbe{
			Operation: row.operation, Projection: row.projection, FTSPhase: row.fts,
			Runs: 25, P95MS: 1.25, ReturnedItems: 20, ReturnedBodyBytes: bodyBytes,
		})
	}
	return BodyFreeEvidence{
		Schema: bodyFreeEvidenceSchema,
		Status: "pass",
		Host: bodyFreeEvidenceHost{
			GOOS: "darwin", GOARCH: "arm64", GoVersion: "go1.26.3",
		},
		Preflight: bodyFreeEvidencePreflight{
			RequiredScratchBytes:  v0330EvidenceRequiredScratchBytes,
			AvailableScratchBytes: v0330EvidenceRequiredScratchBytes,
			Capable:               true,
		},
		PhaseA: &bodyFreeEvidencePhaseA{
			ManagedBytes: 2 << 30, StoredBodyBytes: 2 << 30,
			Events: 8, MissingBodyMetadata: 0,
			OrderedIndex: true, CoveringIndex: true, Runs: 25,
			P95MS: 1.5, TargetP95MS: 250, Passed: true,
		},
		PhaseB: &bodyFreeEvidencePhaseB{
			SourceManagedBytes: 2 << 30, ScratchBytesAfterCheckpoint: 4 << 30,
			Events: 130, MigrationMS: 25, ResumeBackfillMS: 2,
			Migrations31And32: true, IntegrityOK: true, ForeignKeyViolations: 0,
			SourceUnchanged: true, InitialFTSDocuments: 128, InitialFTSComplete: false,
			FinalFTSDocuments: 130, FinalFTSComplete: true,
		},
		PhaseC: probes,
		PhaseD: &bodyFreeEvidencePhaseD{
			MaxItems: 100, MaxAggregateBodyBytes: 64 * 1024,
			ObservedMaxItems: 24, ObservedMaxAggregateBodyBytes: 64_000,
			Pages: 5, TotalItems: 100, MultibyteObserved: true,
			BodyBlocksObserved: true, ContinuationNoDuplicateOrSkip: true,
		},
		PhaseE: &bodyFreeEvidencePhaseE{HostCount: 6, ManifestParity: true},
		Privacy: bodyFreeEvidencePrivacy{
			MetricsOnly: true, ScratchPrivate: true, ScratchCleaned: true,
		},
	}
}
