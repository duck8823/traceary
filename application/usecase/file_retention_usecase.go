package usecase

import (
	"context"
	"math"
	"strconv"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const fileRetentionPlanSchemaVersion = "file-retention-plan/v1"

// FileRetentionCapacityInspector provides read-only operational capacity evidence.
type FileRetentionCapacityInspector interface {
	InspectCapacity(ctx context.Context, request apptypes.FileRetentionCapacityRequest) ([]apptypes.FileRetentionCapacityStatus, error)
}

// FileRetentionUsecase plans and explicitly applies local archive/backup limits.
type FileRetentionUsecase interface {
	FileRetentionCapacityInspector
	CreatePlan(ctx context.Context, request apptypes.FileRetentionPlanRequest, now time.Time) ([]byte, error)
	Apply(ctx context.Context, encodedPlan []byte, confirmedPlanID string, now time.Time) (apptypes.FileRetentionApplyResult, error)
}

func (usecase *fileRetentionUsecase) InspectCapacity(ctx context.Context, request apptypes.FileRetentionCapacityRequest) ([]apptypes.FileRetentionCapacityStatus, error) {
	if usecase.inventory == nil {
		return nil, xerrors.New("file retention inventory is not configured")
	}
	if request.DatabasePath == "" || len(request.Classes) == 0 {
		return nil, xerrors.New("file retention database and at least one class are required")
	}
	statuses := make([]apptypes.FileRetentionCapacityStatus, 0, len(request.Classes))
	for _, classRequest := range request.Classes {
		if classRequest.DatabasePath != "" && classRequest.DatabasePath != request.DatabasePath {
			return nil, xerrors.Errorf("%s capacity database path does not match the request", classRequest.Class)
		}
		snapshot, err := usecase.inventory.InspectFileRetention(ctx, apptypes.FileRetentionInventoryRequest{
			Class: classRequest.Class, Root: classRequest.Root, DatabasePath: request.DatabasePath,
		})
		if err != nil {
			return nil, xerrors.Errorf("inspect %s retention capacity: %w", classRequest.Class, err)
		}
		statuses = append(statuses, summarizeFileRetentionCapacity(snapshot))
	}
	return statuses, nil
}

func summarizeFileRetentionCapacity(snapshot apptypes.FileRetentionInventorySnapshot) apptypes.FileRetentionCapacityStatus {
	status := apptypes.FileRetentionCapacityStatus{
		Class: snapshot.Class, Root: snapshot.Root, FileCount: len(snapshot.Entries), AllocatedKnown: true, RootAccess: snapshot.RootAccess,
	}
	for _, entry := range snapshot.Entries {
		if total, ok := addNonNegativeInt64(status.LogicalBytes, entry.LogicalBytes); ok && !status.LogicalOverflow {
			status.LogicalBytes = total
		} else {
			status.LogicalOverflow = true
		}
		if entry.AllocatedKnown {
			if total, ok := addNonNegativeInt64(status.AllocatedBytes, entry.AllocatedBytes); ok && !status.AllocatedOverflow {
				status.AllocatedBytes = total
			} else {
				status.AllocatedOverflow = true
			}
		} else {
			status.AllocatedKnown = false
		}
		if entry.Verified {
			status.VerifiedCount++
		} else {
			status.UnverifiedCount++
		}
		if entry.BlockingReason != "" {
			status.BlockingCount++
		}
	}
	if len(snapshot.Entries) == 0 {
		status.State = "empty"
		return status
	}
	floorIndex := newestVerifiedCurrentGeneration(snapshot.Entries, snapshot.LiveGeneration)
	if floorIndex >= 0 {
		status.FloorRelativePath = snapshot.Entries[floorIndex].RelativePath
	}
	if floorIndex >= 0 && status.BlockingCount == 0 && status.AllocatedKnown && !status.LogicalOverflow && !status.AllocatedOverflow {
		status.State = "ready"
	} else {
		status.State = "indeterminate"
	}
	return status
}

func addNonNegativeInt64(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || left > math.MaxInt64-right {
		return 0, false
	}
	return left + right, true
}

type fileRetentionUsecase struct {
	inventory application.FileRetentionInventory
	executor  application.FileRetentionExecutor
}

// NewFileRetentionUsecase creates the file-capacity workflow.
func NewFileRetentionUsecase(inventory application.FileRetentionInventory, executor application.FileRetentionExecutor) FileRetentionUsecase {
	return &fileRetentionUsecase{inventory: inventory, executor: executor}
}

func (usecase *fileRetentionUsecase) CreatePlan(ctx context.Context, request apptypes.FileRetentionPlanRequest, now time.Time) ([]byte, error) {
	if usecase.inventory == nil {
		return nil, xerrors.New("file retention inventory is not configured")
	}
	if now.IsZero() || request.ExpiresAfter <= 0 || request.DatabasePath == "" || len(request.Classes) == 0 {
		return nil, xerrors.New("file retention database, class, clock, and expiry are required")
	}
	payload := apptypes.FileRetentionCanonicalPayload{
		SchemaVersion: fileRetentionPlanSchemaVersion,
		CreatedAt:     now.UTC().Format(time.RFC3339Nano),
		ExpiresAt:     now.UTC().Add(request.ExpiresAfter).Format(time.RFC3339Nano),
		DatabasePath:  request.DatabasePath,
		Classes:       make([]apptypes.FileRetentionClassPlan, 0, len(request.Classes)),
	}
	for _, classRequest := range request.Classes {
		snapshot, err := usecase.inventory.InspectFileRetention(ctx, apptypes.FileRetentionInventoryRequest{
			Class: classRequest.Class, Root: classRequest.Root, DatabasePath: request.DatabasePath,
		})
		if err != nil {
			return nil, xerrors.Errorf("inspect %s retention root: %w", classRequest.Class, err)
		}
		classPlan, err := buildFileRetentionClassPlan(snapshot, classRequest.Budget, now)
		if err != nil {
			return nil, xerrors.Errorf("build %s retention plan: %w", classRequest.Class, err)
		}
		payload.Classes = append(payload.Classes, classPlan)
	}
	return encodeFileRetentionPlan(apptypes.FileRetentionPlan{
		CanonicalPayload: payload,
		Display:          apptypes.FileRetentionPlanDisplay{Summary: "Review exact file identities before apply."},
	})
}

func (usecase *fileRetentionUsecase) Apply(ctx context.Context, encodedPlan []byte, confirmedPlanID string, now time.Time) (apptypes.FileRetentionApplyResult, error) {
	if usecase.executor == nil {
		return apptypes.FileRetentionApplyResult{}, xerrors.New("file retention executor is not configured")
	}
	plan, err := decodeFileRetentionPlan(encodedPlan)
	if err != nil {
		return apptypes.FileRetentionApplyResult{}, err
	}
	if confirmedPlanID != plan.PlanID {
		return apptypes.FileRetentionApplyResult{}, xerrors.New("confirmed file retention plan ID does not match")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, plan.CanonicalPayload.ExpiresAt)
	if err != nil || now.UTC().After(expiresAt) {
		return apptypes.FileRetentionApplyResult{}, xerrors.New("file retention plan has expired")
	}
	for _, classPlan := range plan.CanonicalPayload.Classes {
		if classPlan.Status != "satisfied" {
			return apptypes.FileRetentionApplyResult{}, xerrors.Errorf("file retention class %s is %s and cannot be applied", classPlan.Class, classPlan.Status)
		}
	}
	result, err := usecase.executor.ApplyFileRetention(ctx, plan, confirmedPlanID, now)
	if err != nil {
		return apptypes.FileRetentionApplyResult{}, xerrors.Errorf("execute file retention plan: %w", err)
	}
	return result, nil
}

func buildFileRetentionClassPlan(snapshot apptypes.FileRetentionInventorySnapshot, input apptypes.FileRetentionBudgetInput, now time.Time) (apptypes.FileRetentionClassPlan, error) {
	budgetParams := model.FileCapacityBudgetParams{}
	budgetPlan := apptypes.FileRetentionBudgetPlan{}
	if input.MaxAge != nil {
		budgetParams.MaxAge = types.Some(*input.MaxAge)
		budgetPlan.MaxAgeSeconds = strconv.FormatInt(int64(input.MaxAge.Seconds()), 10)
	}
	if input.MaxCount != nil {
		budgetParams.MaxCount = types.Some(*input.MaxCount)
		budgetPlan.MaxCount = strconv.Itoa(*input.MaxCount)
	}
	if input.MaxAllocatedBytes != nil {
		budgetParams.MaxAllocatedBytes = types.Some(*input.MaxAllocatedBytes)
		budgetPlan.MaxAllocatedByte = strconv.FormatInt(*input.MaxAllocatedBytes, 10)
	}
	budget, err := model.NewFileCapacityBudget(budgetParams)
	if err != nil {
		return apptypes.FileRetentionClassPlan{}, xerrors.Errorf("validate file retention budget: %w", err)
	}

	floorIndex := newestVerifiedCurrentGeneration(snapshot.Entries, snapshot.LiveGeneration)
	domainEntries := make([]model.FileRetentionEntry, 0, len(snapshot.Entries))
	inventory := make([]apptypes.FileRetentionInventoryPlan, 0, len(snapshot.Entries))
	for index, entry := range snapshot.Entries {
		protected := index == floorIndex
		domainEntry, err := model.NewFileRetentionEntry(model.FileRetentionEntryParams{
			Identity: entry.Identity, RelativePath: entry.RelativePath, CreatedAt: entry.GenerationCreatedAt,
			Generation: entry.Generation, ContentDigest: entry.ContentSHA256, AllocatedBytes: entry.AllocatedBytes,
			AllocatedKnown: entry.AllocatedKnown, Verified: entry.Verified, Protected: protected,
			Pinned: entry.Pinned, BlockingReason: entry.BlockingReason,
		})
		if err != nil {
			return apptypes.FileRetentionClassPlan{}, xerrors.Errorf("restore file retention policy entry: %w", err)
		}
		domainEntries = append(domainEntries, domainEntry)
		inventory = append(inventory, fileRetentionInventoryPlan(entry, protected))
	}
	decision := model.DecideFileRetention(domainEntries, budget, now)
	status := decision.Status()
	if floorIndex < 0 && len(snapshot.Entries) > 0 && status == "satisfied" {
		status = "indeterminate"
	}
	classPlan := apptypes.FileRetentionClassPlan{
		Class: snapshot.Class, Root: snapshot.Root, RootIdentity: snapshot.RootIdentity,
		LiveGeneration: snapshot.LiveGeneration, Budget: budgetPlan, Inventory: inventory, Status: status,
		OrderedSteps: []string{"verify-plan", "acquire-root-lock", "verify-inventory", "verify-floor", "reserve-catalog", "rename-tombstone", "verify-tombstone", "unlink-tombstone", "record-ledger", "commit-catalog"},
	}
	for _, ceiling := range decision.Ceilings() {
		classPlan.Ceilings = append(classPlan.Ceilings, apptypes.FileRetentionCeilingPlan{
			Ceiling: ceiling.Ceiling(), Current: strconv.FormatInt(ceiling.Current(), 10), Projected: strconv.FormatInt(ceiling.Projected(), 10),
		})
	}
	if floorIndex >= 0 {
		floor := snapshot.Entries[floorIndex]
		classPlan.Floor = &apptypes.FileRetentionFloorPlan{
			Identity: floor.Identity, RelativePath: floor.RelativePath, Generation: floor.Generation,
			ContentSHA256: floor.ContentSHA256, VerificationDigest: floor.VerificationDigest,
		}
	}
	if status == "satisfied" {
		for index, candidate := range decision.Candidates() {
			classPlan.Candidates = append(classPlan.Candidates, apptypes.FileRetentionCandidatePlan{
				Identity: candidate.Entry().Identity(), RelativePath: candidate.Entry().RelativePath(), Reasons: candidate.Reasons(),
			})
			classPlan.Batches = append(classPlan.Batches, apptypes.FileRetentionBatchPlan{Ordinal: strconv.Itoa(index), Identity: candidate.Entry().Identity()})
		}
	}
	return classPlan, nil
}

func newestVerifiedCurrentGeneration(entries []apptypes.FileRetentionInventoryEntry, liveGeneration string) int {
	selected := -1
	for index, entry := range entries {
		if !entry.Verified || entry.Generation != liveGeneration || entry.BlockingReason != "" {
			continue
		}
		if selected < 0 || entries[selected].GenerationCreatedAt.Before(entry.GenerationCreatedAt) ||
			(entries[selected].GenerationCreatedAt.Equal(entry.GenerationCreatedAt) && entries[selected].ContentSHA256 < entry.ContentSHA256) ||
			(entries[selected].GenerationCreatedAt.Equal(entry.GenerationCreatedAt) && entries[selected].ContentSHA256 == entry.ContentSHA256 && entries[selected].RelativePath < entry.RelativePath) {
			selected = index
		}
	}
	return selected
}

func fileRetentionInventoryPlan(entry apptypes.FileRetentionInventoryEntry, protected bool) apptypes.FileRetentionInventoryPlan {
	allocated := ""
	if entry.AllocatedKnown {
		allocated = strconv.FormatInt(entry.AllocatedBytes, 10)
	}
	return apptypes.FileRetentionInventoryPlan{
		Identity: entry.Identity, RelativePath: entry.RelativePath, Device: strconv.FormatUint(entry.Device, 10),
		Inode: strconv.FormatUint(entry.Inode, 10), LinkCount: strconv.FormatUint(entry.LinkCount, 10),
		LogicalBytes: strconv.FormatInt(entry.LogicalBytes, 10), AllocatedBytes: allocated, AllocatedKnown: entry.AllocatedKnown,
		ModifiedAt: entry.ModifiedAt.UTC().Format(time.RFC3339Nano), GenerationCreatedAt: entry.GenerationCreatedAt.UTC().Format(time.RFC3339Nano),
		GenerationProvenance: entry.GenerationProvenance, Generation: entry.Generation, ContentSHA256: entry.ContentSHA256,
		Verified: entry.Verified, VerificationDigest: entry.VerificationDigest, VerificationReason: entry.VerificationReason,
		MetadataRelativePath: entry.MetadataRelativePath, MetadataSHA256: entry.MetadataSHA256,
		Pinned: entry.Pinned, Protected: protected, BlockingReason: entry.BlockingReason,
	}
}
