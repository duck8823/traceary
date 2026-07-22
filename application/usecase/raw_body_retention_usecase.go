package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
)

// RawBodyRetentionUsecase creates and executes reviewed raw-body plans.
type RawBodyRetentionUsecase interface {
	CreatePlan(ctx context.Context, before time.Time, recoveryPath string, now time.Time) ([]byte, error)
	Apply(ctx context.Context, planData []byte, recoveryPath, confirmedPlanID string, now time.Time) (apptypes.RawBodyApplyResult, error)
	Restore(ctx context.Context, planData []byte, recoveryPath, confirmedPlanID string, now time.Time) (apptypes.RawBodyRestoreResult, error)
}

type rawBodyRetentionUsecase struct {
	planner  application.RawBodyRetentionPlanner
	executor application.RawBodyRetentionExecutor
}

// NewRawBodyRetentionUsecase creates the explicit plan/apply/restore workflow.
func NewRawBodyRetentionUsecase(planner application.RawBodyRetentionPlanner, executor application.RawBodyRetentionExecutor) RawBodyRetentionUsecase {
	return &rawBodyRetentionUsecase{planner: planner, executor: executor}
}

func (u *rawBodyRetentionUsecase) CreatePlan(ctx context.Context, before time.Time, recoveryPath string, now time.Time) ([]byte, error) {
	if u.planner == nil {
		return nil, xerrors.Errorf("raw-body retention planner is not configured")
	}
	if before.IsZero() || !before.Before(now) {
		return nil, xerrors.Errorf("retention cutoff must be before the plan time")
	}
	snapshot, err := u.planner.ListRawBodyCandidates(ctx, before)
	if err != nil {
		return nil, xerrors.Errorf("list raw-body candidates: %w", err)
	}
	recovery, recoveryDigest, err := loadRawBodyRecovery(recoveryPath, snapshot.Candidates)
	if err != nil {
		return nil, err
	}
	_ = recovery

	candidates := make([]apptypes.RetentionPlanCandidate, 0, len(snapshot.Candidates))
	identities := make([]string, 0, len(snapshot.Candidates))
	totalBytes := 0
	for _, candidate := range snapshot.Candidates {
		identity := rawBodyCandidateIdentity(candidate.EventID, candidate.BodySHA256)
		identities = append(identities, identity)
		totalBytes += candidate.StoredBytes
		candidates = append(candidates, apptypes.RetentionPlanCandidate{
			Class: "raw_body", IdentityKind: "database", DatabaseIdentity: candidate.EventID,
			RootID: "", RelativePath: "", Timestamp: candidate.CreatedAt.UTC().Format(time.RFC3339Nano), CandidateIdentity: identity,
			LogicalExtent:   apptypes.RetentionExtent{Availability: "known", Bytes: strconv.Itoa(candidate.StoredBytes)},
			AllocatedExtent: apptypes.RetentionExtent{Availability: "unknown"}, Reasons: []string{"age"},
		})
	}
	exclusions := make([]apptypes.RetentionPlanExclusion, 0, len(snapshot.ExcludedActive))
	for _, eventID := range snapshot.ExcludedActive {
		exclusions = append(exclusions, apptypes.RetentionPlanExclusion{Reason: "active_session", StableIdentity: "event:" + base64.RawURLEncoding.EncodeToString([]byte(eventID))})
	}
	coverage := sha256.Sum256([]byte(joinIdentities(identities)))
	rootPath, err := filepath.Abs(filepath.Dir(recoveryPath))
	if err != nil {
		return nil, xerrors.Errorf("resolve recovery root: %w", err)
	}
	rootDigest := sha256.Sum256([]byte(rootPath))
	migrationDigest := sha256.Sum256([]byte("traceary-sqlite-user-version:" + strconv.Itoa(snapshot.SQLiteUserVersion)))
	status := "satisfied"
	if len(candidates) > 0 {
		status = "unsatisfied"
	}
	plan := apptypes.RetentionPlan{
		CanonicalPayload: apptypes.RetentionCanonicalPayload{
			SchemaVersion: retentionPlanSchemaVersion,
			CreatedAt:     now.UTC().Format(time.RFC3339Nano), SnapshotAt: snapshot.SnapshotAt.UTC().Format(time.RFC3339Nano),
			Source: apptypes.RetentionPlanSource{
				DatabaseIdentity: snapshot.DatabaseIdentity, SQLiteUserVersion: snapshot.SQLiteUserVersion,
				MigrationDigest: hex.EncodeToString(migrationDigest[:]),
				Roots:           []apptypes.RetentionPlanRoot{{RootID: "recovery", Fingerprint: hex.EncodeToString(rootDigest[:])}},
			},
			Policy: apptypes.RetentionPlanPolicy{Ceilings: []apptypes.RetentionPolicyCeiling{{Class: "raw_body", Ceiling: "age", Value: strconv.FormatInt(now.Sub(before).Nanoseconds(), 10)}}},
			ClassResults: []apptypes.RetentionClassResult{{
				Class: "raw_body", Status: status,
				Ceilings: []apptypes.RetentionCeilingResult{{Ceiling: "age", Status: status, Current: apptypes.RetentionExtent{Availability: "known", Bytes: strconv.Itoa(totalBytes)}, Projected: apptypes.RetentionExtent{Availability: "known", Bytes: "0"}}},
			}},
			Candidates: candidates, Exclusions: exclusions,
			RecoveryRequirements: []apptypes.RetentionRecoveryPoint{{
				Generation: "archive-" + recoveryDigest[:12], Digest: recoveryDigest, RootID: "recovery", RelativePath: filepath.Base(recoveryPath),
				CoverageDigest: hex.EncodeToString(coverage[:]), State: "active",
			}},
			Phases: []apptypes.RetentionPlanPhase{{Phase: "body_prune", Batches: []apptypes.RetentionPlanBatch{{Ordinal: "0", CandidateIdentities: identities}}, OrderedSteps: []string{"verify-source", "verify-recovery", "prune-body", "record-ledger"}}},
		},
		Display: apptypes.RetentionPlanDisplay{Summary: strconv.Itoa(len(candidates)) + " raw-body candidates, " + strconv.Itoa(len(exclusions)) + " active-session exclusions"},
	}
	return encodeRetentionPlan(plan)
}

func (u *rawBodyRetentionUsecase) Apply(ctx context.Context, planData []byte, recoveryPath, confirmedPlanID string, now time.Time) (apptypes.RawBodyApplyResult, error) {
	if u.executor == nil {
		return apptypes.RawBodyApplyResult{}, xerrors.Errorf("raw-body retention executor is not configured")
	}
	plan, candidates, bodies, err := u.prepareExecution(planData, recoveryPath)
	if err != nil {
		return apptypes.RawBodyApplyResult{}, err
	}
	if plan.PlanID != confirmedPlanID {
		return apptypes.RawBodyApplyResult{}, xerrors.Errorf("confirmed plan ID does not match reviewed plan")
	}
	_ = bodies
	result, err := u.executor.ApplyRawBodyPlan(ctx, plan.CanonicalPayload.Source.DatabaseIdentity, plan.CanonicalPayload.Source.SQLiteUserVersion, plan.PlanID, candidates, now.UTC())
	if err != nil {
		return apptypes.RawBodyApplyResult{}, xerrors.Errorf("apply reviewed raw-body plan: %w", err)
	}
	return result, nil
}

func (u *rawBodyRetentionUsecase) Restore(ctx context.Context, planData []byte, recoveryPath, confirmedPlanID string, now time.Time) (apptypes.RawBodyRestoreResult, error) {
	if u.executor == nil {
		return apptypes.RawBodyRestoreResult{}, xerrors.Errorf("raw-body retention executor is not configured")
	}
	plan, _, bodies, err := u.prepareExecution(planData, recoveryPath)
	if err != nil {
		return apptypes.RawBodyRestoreResult{}, err
	}
	if plan.PlanID != confirmedPlanID {
		return apptypes.RawBodyRestoreResult{}, xerrors.Errorf("confirmed plan ID does not match reviewed plan")
	}
	result, err := u.executor.RestoreRawBodyPlan(ctx, plan.CanonicalPayload.Source.DatabaseIdentity, plan.CanonicalPayload.Source.SQLiteUserVersion, plan.PlanID, bodies, now.UTC())
	if err != nil {
		return apptypes.RawBodyRestoreResult{}, xerrors.Errorf("restore reviewed raw-body plan: %w", err)
	}
	return result, nil
}

func (u *rawBodyRetentionUsecase) prepareExecution(planData []byte, recoveryPath string) (apptypes.RetentionPlan, []apptypes.RawBodyCandidate, []apptypes.RawBodyRecoveryBody, error) {
	plan, err := decodeRetentionPlan(planData)
	if err != nil {
		return apptypes.RetentionPlan{}, nil, nil, err
	}
	candidates := make([]apptypes.RawBodyCandidate, 0, len(plan.CanonicalPayload.Candidates))
	for _, planned := range plan.CanonicalPayload.Candidates {
		eventID, digest, err := parseRawBodyCandidateIdentity(planned.CandidateIdentity)
		if err != nil || eventID != planned.DatabaseIdentity {
			return apptypes.RetentionPlan{}, nil, nil, xerrors.Errorf("retention candidate identity does not match database identity")
		}
		createdAt, err := time.Parse(time.RFC3339Nano, planned.Timestamp)
		if err != nil {
			return apptypes.RetentionPlan{}, nil, nil, xerrors.Errorf("parse retention candidate timestamp: %w", err)
		}
		storedBytes, err := strconv.Atoi(planned.LogicalExtent.Bytes)
		if err != nil || storedBytes < 0 {
			return apptypes.RetentionPlan{}, nil, nil, xerrors.Errorf("invalid retention candidate stored bytes")
		}
		candidates = append(candidates, apptypes.RawBodyCandidate{EventID: eventID, CreatedAt: createdAt, StoredBytes: storedBytes, BodySHA256: digest})
	}
	bodies, recoveryDigest, err := loadRawBodyRecovery(recoveryPath, candidates)
	if err != nil {
		return apptypes.RetentionPlan{}, nil, nil, err
	}
	if recoveryDigest != plan.CanonicalPayload.RecoveryRequirements[0].Digest {
		return apptypes.RetentionPlan{}, nil, nil, xerrors.Errorf("recovery package digest does not match reviewed plan")
	}
	return plan, candidates, bodies, nil
}

func loadRawBodyRecovery(path string, candidates []apptypes.RawBodyCandidate) ([]apptypes.RawBodyRecoveryBody, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", xerrors.Errorf("read recovery package: %w", err)
	}
	digest := sha256.Sum256(data)
	manifest, files, err := openStoreArchivePackage(data, nil)
	if err != nil {
		return nil, "", xerrors.Errorf("open recovery package: %w", err)
	}
	if err := verifyStoreArchiveContents(manifest, files); err != nil {
		return nil, "", xerrors.Errorf("verify recovery package: %w", err)
	}
	tables, err := parseStoreArchiveTables(manifest, files)
	if err != nil {
		return nil, "", xerrors.Errorf("parse recovery package: %w", err)
	}
	byID := make(map[string]string)
	for _, row := range tables["events"] {
		id, idOK := row["id"].(string)
		body, bodyOK := row["body"].(string)
		if idOK && bodyOK {
			byID[id] = body
		}
	}
	bodies := make([]apptypes.RawBodyRecoveryBody, 0, len(candidates))
	for _, candidate := range candidates {
		body, ok := byID[candidate.EventID]
		if !ok {
			return nil, "", xerrors.Errorf("recovery package does not cover event %s", candidate.EventID)
		}
		bodyDigest := sha256.Sum256([]byte(body))
		if len(body) != candidate.StoredBytes || hex.EncodeToString(bodyDigest[:]) != candidate.BodySHA256 {
			return nil, "", xerrors.Errorf("recovery package body does not match event %s", candidate.EventID)
		}
		bodies = append(bodies, apptypes.RawBodyRecoveryBody{Candidate: candidate, Body: body})
	}
	return bodies, hex.EncodeToString(digest[:]), nil
}

func joinIdentities(identities []string) string {
	result := ""
	for index, identity := range identities {
		if index > 0 {
			result += "\n"
		}
		result += identity
	}
	return result
}
