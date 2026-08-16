package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestRawBodyRetentionPlanStoresEncodedExtentsAndProjectedMarkers(t *testing.T) {
	t.Parallel()

	const (
		compressedID       = "retention-compressed"
		legacyID           = "retention-legacy"
		markerEncodedBytes = 37 // measured by encodePayload(marker, payloadCodecIdentity)
		currentBytes       = 44 + 9
		projectedBytes     = markerEncodedBytes * 2
	)
	if markerEncodedBytes != len([]byte(domtypes.EventBodyUnavailableRetentionMarker)) {
		t.Fatalf("retention marker encoded size fixture is stale")
	}
	compressedBody := strings.Repeat("compressible body ", 4096)
	legacyBody := "日本語"
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	createdAt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		compressedBody string
		legacyBody     string
	}{
		{name: "compressed and legacy candidates", compressedBody: compressedBody, legacyBody: legacyBody},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recoveryPath := filepath.Join(t.TempDir(), "recovery.archive")
			candidates := retentionTestCandidates(compressedID, test.compressedBody, legacyID, test.legacyBody, createdAt)
			writeRetentionTestRecovery(t, recoveryPath, candidates, test.compressedBody, test.legacyBody)
			planner := &retentionPlannerStub{markerEncodedBytes: markerEncodedBytes, snapshot: apptypes.RawBodyRetentionSnapshot{
				DatabaseIdentity: testDigest("db"), SQLiteUserVersion: 53, MigrationDigest: testDigest("migration"), SnapshotAt: now,
				Candidates: candidates,
			}}
			executor := &retentionExecutorStub{}
			workflow := NewRawBodyRetentionUsecase(planner, executor)

			planData, err := workflow.CreatePlan(context.Background(), createdAt.Add(time.Hour), recoveryPath, now)
			if err != nil {
				t.Fatalf("CreatePlan() error = %v", err)
			}
			plan, err := decodeRetentionPlan(planData)
			if err != nil {
				t.Fatalf("decodeRetentionPlan() error = %v", err)
			}
			got := struct {
				Current    string
				Projected  string
				Candidates []string
			}{}
			for _, candidate := range plan.CanonicalPayload.Candidates {
				got.Candidates = append(got.Candidates, candidate.LogicalExtent.Bytes)
			}
			want := struct {
				Current    string
				Projected  string
				Candidates []string
			}{Current: "53", Projected: "74", Candidates: []string{"44", "9"}}
			got.Current = plan.CanonicalPayload.ClassResults[0].Ceilings[0].Current.Bytes
			got.Projected = plan.CanonicalPayload.ClassResults[0].Ceilings[0].Projected.Bytes
			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("retention plan extents mismatch (-want +got):\n%s", diff)
			}
			if got.Current != strconv.Itoa(currentBytes) || got.Projected != strconv.Itoa(projectedBytes) {
				t.Fatalf("retention plan totals are not exact: current=%s projected=%s", got.Current, got.Projected)
			}

			if _, err := workflow.Apply(context.Background(), planData, recoveryPath, plan.PlanID, now.Add(time.Hour)); err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if diff := cmp.Diff([]int{44, 9}, executor.candidates); diff != "" {
				t.Fatalf("apply candidate extents mismatch (-want +got):\n%s", diff)
			}
			wantCutoff := createdAt.Add(time.Hour)
			if !executor.cutoffAtSeen || !executor.cutoffAt.Equal(wantCutoff) {
				t.Fatalf("apply cutoff = %s (seen=%t), want the plan's persisted cutoff %s, not the apply-time clock", executor.cutoffAt, executor.cutoffAtSeen, wantCutoff)
			}
		})
	}
}

func TestRawBodyRetentionApply_FallsBackToCreatedAtWhenCutoffAtMissing(t *testing.T) {
	t.Parallel()

	const (
		compressedID       = "retention-compressed"
		legacyID           = "retention-legacy"
		markerEncodedBytes = 37
	)
	compressedBody := strings.Repeat("compressible body ", 4096)
	legacyBody := "日本語"
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	createdAt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	recoveryPath := filepath.Join(t.TempDir(), "recovery.archive")
	candidates := retentionTestCandidates(compressedID, compressedBody, legacyID, legacyBody, createdAt)
	writeRetentionTestRecovery(t, recoveryPath, candidates, compressedBody, legacyBody)
	planner := &retentionPlannerStub{markerEncodedBytes: markerEncodedBytes, snapshot: apptypes.RawBodyRetentionSnapshot{
		DatabaseIdentity: testDigest("db"), SQLiteUserVersion: 53, MigrationDigest: testDigest("migration"), SnapshotAt: now,
		Candidates: candidates,
	}}
	executor := &retentionExecutorStub{}
	workflow := NewRawBodyRetentionUsecase(planner, executor)

	planData, err := workflow.CreatePlan(context.Background(), createdAt.Add(time.Hour), recoveryPath, now)
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	plan, err := decodeRetentionPlan(planData)
	if err != nil {
		t.Fatalf("decodeRetentionPlan() error = %v", err)
	}

	// Simulate a plan written before cutoff_at existed: strip it and re-sign,
	// as an old plan on disk would already have no such field.
	plan.CanonicalPayload.CutoffAt = ""
	canonical, err := canonicalRetentionPayload(plan.CanonicalPayload)
	if err != nil {
		t.Fatalf("canonicalRetentionPayload() error = %v", err)
	}
	digest := sha256.Sum256(canonical)
	plan.PlanID = hex.EncodeToString(digest[:])
	legacyPlanData, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal legacy plan: %v", err)
	}

	if _, err := workflow.Apply(context.Background(), legacyPlanData, recoveryPath, plan.PlanID, now.Add(time.Hour)); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	// The plan's created_at is the fallback, not its selection cutoff (before)
	// nor the apply-time clock: it is the only timestamp a pre-migration plan
	// still carries.
	wantCutoff := now
	if !executor.cutoffAtSeen || !executor.cutoffAt.Equal(wantCutoff) {
		t.Fatalf("apply cutoff = %s (seen=%t), want fallback to created_at %s", executor.cutoffAt, executor.cutoffAtSeen, wantCutoff)
	}
}

func retentionTestCandidates(compressedID, compressedBody, legacyID, legacyBody string, createdAt time.Time) []apptypes.RawBodyCandidate {
	return []apptypes.RawBodyCandidate{
		{EventID: compressedID, CreatedAt: createdAt, EncodedBytes: 44, PlaintextBytes: len([]byte(compressedBody)), BodySHA256: testDigest(compressedBody)},
		{EventID: legacyID, CreatedAt: createdAt, EncodedBytes: len([]byte(legacyBody)), PlaintextBytes: len([]byte(legacyBody)), BodySHA256: testDigest(legacyBody)},
	}
}

func writeRetentionTestRecovery(t *testing.T, path string, candidates []apptypes.RawBodyCandidate, compressedBody, legacyBody string) {
	t.Helper()
	rows := []map[string]any{
		{"id": candidates[0].EventID, "created_at": candidates[0].CreatedAt.Format(time.RFC3339Nano), "body": compressedBody},
		{"id": candidates[1].EventID, "created_at": candidates[1].CreatedAt.Format(time.RFC3339Nano), "body": legacyBody},
	}
	data, _, err := buildStoreArchivePackage([]storeArchiveTableData{{Name: "events", PrimaryKey: []string{"id"}, Rows: rows}}, storeArchivePlan{}, "test", "", nil)
	if err != nil {
		t.Fatalf("buildStoreArchivePackage() error = %v", err)
	}
	if err := writeFileForRetentionTest(path, data); err != nil {
		t.Fatalf("write recovery package: %v", err)
	}
}

func writeFileForRetentionTest(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return xerrors.Errorf("write retention test archive: %w", err)
	}
	return nil
}

func testDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

type retentionPlannerStub struct {
	snapshot           apptypes.RawBodyRetentionSnapshot
	markerEncodedBytes int
}

func (s *retentionPlannerStub) ListRawBodyCandidates(context.Context, time.Time) (apptypes.RawBodyRetentionSnapshot, error) {
	return s.snapshot, nil
}

func (s *retentionPlannerStub) RetentionMarkerEncodedBytes() (int, error) {
	return s.markerEncodedBytes, nil
}

type retentionExecutorStub struct {
	candidates   []int
	cutoffAt     time.Time
	cutoffAtSeen bool
}

func (s *retentionExecutorStub) ApplyRawBodyPlan(_ context.Context, _ string, _ int, _, _ string, candidates []apptypes.RawBodyCandidate, cutoffAt, _ time.Time) (apptypes.RawBodyApplyResult, error) {
	for _, candidate := range candidates {
		s.candidates = append(s.candidates, candidate.EncodedBytes)
	}
	s.cutoffAt = cutoffAt
	s.cutoffAtSeen = true
	return apptypes.RawBodyApplyResult{}, nil
}

func (s *retentionExecutorStub) RestoreRawBodyPlan(context.Context, string, int, string, string, []apptypes.RawBodyRecoveryBody, time.Time) (apptypes.RawBodyRestoreResult, error) {
	return apptypes.RawBodyRestoreResult{}, nil
}

var _ application.RawBodyRetentionPlanner = (*retentionPlannerStub)(nil)
var _ application.RawBodyRetentionExecutor = (*retentionExecutorStub)(nil)
