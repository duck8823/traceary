package usecase

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func encodeFileRetentionPlan(plan apptypes.FileRetentionPlan) ([]byte, error) {
	normalizeFileRetentionPlan(&plan)
	canonical, err := canonicalFileRetentionPayload(plan.CanonicalPayload)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(canonical)
	plan.PlanID = hex.EncodeToString(digest[:])
	if err := validateFileRetentionPlan(plan); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return nil, xerrors.Errorf("marshal file retention plan: %w", err)
	}
	return append(encoded, '\n'), nil
}

func decodeFileRetentionPlan(data []byte) (apptypes.FileRetentionPlan, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var plan apptypes.FileRetentionPlan
	if err := decoder.Decode(&plan); err != nil {
		return apptypes.FileRetentionPlan{}, xerrors.Errorf("parse file retention plan: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return apptypes.FileRetentionPlan{}, err
	}
	if err := validateFileRetentionPlan(plan); err != nil {
		return apptypes.FileRetentionPlan{}, err
	}
	canonical, err := canonicalFileRetentionPayload(plan.CanonicalPayload)
	if err != nil {
		return apptypes.FileRetentionPlan{}, err
	}
	digest := sha256.Sum256(canonical)
	if plan.PlanID != hex.EncodeToString(digest[:]) {
		return apptypes.FileRetentionPlan{}, xerrors.New("file retention plan ID does not match canonical payload")
	}
	clone := plan
	normalizeFileRetentionPlan(&clone)
	left, _ := json.Marshal(plan.CanonicalPayload)
	right, _ := json.Marshal(clone.CanonicalPayload)
	if !bytes.Equal(left, right) {
		return apptypes.FileRetentionPlan{}, xerrors.New("file retention plan arrays are not in canonical order")
	}
	return plan, nil
}

func canonicalFileRetentionPayload(payload apptypes.FileRetentionCanonicalPayload) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, xerrors.Errorf("marshal file retention canonical payload: %w", err)
	}
	return canonicalizeJSON(raw)
}

func validateFileRetentionPlan(plan apptypes.FileRetentionPlan) error {
	if !lowerHexDigest.MatchString(plan.PlanID) || plan.CanonicalPayload.SchemaVersion != fileRetentionPlanSchemaVersion {
		return xerrors.New("invalid file retention plan ID or schema")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, plan.CanonicalPayload.CreatedAt)
	if err != nil || createdAt.IsZero() {
		return xerrors.New("invalid file retention plan creation time")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, plan.CanonicalPayload.ExpiresAt)
	if err != nil || !expiresAt.After(createdAt) || plan.CanonicalPayload.DatabasePath == "" || len(plan.CanonicalPayload.Classes) == 0 {
		return xerrors.New("invalid file retention plan expiry, database, or classes")
	}
	seenClasses := make(map[string]struct{}, len(plan.CanonicalPayload.Classes))
	for _, classPlan := range plan.CanonicalPayload.Classes {
		if classPlan.Class != "archive" && classPlan.Class != "backup" {
			return xerrors.Errorf("unsupported file retention class %q", classPlan.Class)
		}
		if _, exists := seenClasses[classPlan.Class]; exists {
			return xerrors.Errorf("duplicate file retention class %q", classPlan.Class)
		}
		seenClasses[classPlan.Class] = struct{}{}
		if classPlan.Root == "" || !lowerHexDigest.MatchString(classPlan.RootIdentity) || !lowerHexDigest.MatchString(classPlan.LiveGeneration) {
			return xerrors.New("invalid file retention root or generation")
		}
		if classPlan.Status != "satisfied" && classPlan.Status != "unsatisfied" && classPlan.Status != "indeterminate" {
			return xerrors.New("invalid file retention class status")
		}
		if err := validateFileRetentionClassOrder(classPlan); err != nil {
			return err
		}
		if err := validateFileRetentionDecision(classPlan, createdAt); err != nil {
			return err
		}
	}
	return nil
}

func validateFileRetentionDecision(classPlan apptypes.FileRetentionClassPlan, planTime time.Time) error {
	budgetParams := model.FileCapacityBudgetParams{}
	configured := 0
	if classPlan.Budget.MaxAgeSeconds != "" {
		value, err := strconv.ParseInt(classPlan.Budget.MaxAgeSeconds, 10, 64)
		if err != nil || value < 0 {
			return xerrors.New("invalid file retention max age")
		}
		budgetParams.MaxAge = domtypes.Some(time.Duration(value) * time.Second)
		configured++
	}
	if classPlan.Budget.MaxCount != "" {
		value, err := strconv.Atoi(classPlan.Budget.MaxCount)
		if err != nil || value < 0 {
			return xerrors.New("invalid file retention max count")
		}
		budgetParams.MaxCount = domtypes.Some(value)
		configured++
	}
	if classPlan.Budget.MaxAllocatedByte != "" {
		value, err := strconv.ParseInt(classPlan.Budget.MaxAllocatedByte, 10, 64)
		if err != nil || value < 0 {
			return xerrors.New("invalid file retention allocated-byte ceiling")
		}
		budgetParams.MaxAllocatedBytes = domtypes.Some(value)
		configured++
	}
	if configured == 0 {
		return xerrors.New("file retention class requires at least one ceiling")
	}
	budget, err := model.NewFileCapacityBudget(budgetParams)
	if err != nil {
		return xerrors.Errorf("validate file retention budget decision: %w", err)
	}

	floorIndex := newestFileRetentionFloorPlan(classPlan)
	domainEntries := make([]model.FileRetentionEntry, 0, len(classPlan.Inventory))
	for index, entry := range classPlan.Inventory {
		createdAt, err := time.Parse(time.RFC3339Nano, entry.GenerationCreatedAt)
		if err != nil {
			return xerrors.Errorf("parse file retention inventory generation time: %w", err)
		}
		allocated := int64(0)
		if entry.AllocatedKnown {
			allocated, err = strconv.ParseInt(entry.AllocatedBytes, 10, 64)
			if err != nil || allocated < 0 {
				return xerrors.New("invalid file retention allocated extent")
			}
		} else if entry.AllocatedBytes != "" {
			return xerrors.New("unknown file retention extent must not contain bytes")
		}
		if entry.Protected != (index == floorIndex) {
			return xerrors.New("file retention protected flag does not match newest verified floor")
		}
		domainEntry, err := model.NewFileRetentionEntry(model.FileRetentionEntryParams{
			Identity: entry.Identity, RelativePath: entry.RelativePath, CreatedAt: createdAt, Generation: entry.Generation,
			ContentDigest: entry.ContentSHA256, AllocatedBytes: allocated, AllocatedKnown: entry.AllocatedKnown,
			Verified: entry.Verified, Protected: entry.Protected, Pinned: entry.Pinned, BlockingReason: entry.BlockingReason,
		})
		if err != nil {
			return xerrors.Errorf("restore file retention decision entry: %w", err)
		}
		domainEntries = append(domainEntries, domainEntry)
	}
	decision := model.DecideFileRetention(domainEntries, budget, planTime)
	wantStatus := decision.Status()
	if floorIndex < 0 && len(classPlan.Inventory) > 0 && wantStatus == "satisfied" {
		wantStatus = "indeterminate"
	}
	if classPlan.Status != wantStatus {
		return xerrors.Errorf("file retention class status %q does not match reconstructed %q", classPlan.Status, wantStatus)
	}
	if len(classPlan.Ceilings) != len(decision.Ceilings()) {
		return xerrors.New("file retention ceiling projections do not match reconstructed decision")
	}
	for index, expected := range decision.Ceilings() {
		actual := classPlan.Ceilings[index]
		if actual.Ceiling != expected.Ceiling() || actual.Current != strconv.FormatInt(expected.Current(), 10) || actual.Projected != strconv.FormatInt(expected.Projected(), 10) {
			return xerrors.New("file retention ceiling projection does not match reconstructed decision")
		}
	}
	wantCandidates := decision.Candidates()
	if wantStatus != "satisfied" {
		wantCandidates = nil
	}
	if len(classPlan.Candidates) != len(wantCandidates) {
		return xerrors.New("file retention candidates do not match reconstructed decision")
	}
	for index, expected := range wantCandidates {
		actual := classPlan.Candidates[index]
		if actual.Identity != expected.Entry().Identity() || actual.RelativePath != expected.Entry().RelativePath() || !equalFileRetentionStrings(actual.Reasons, expected.Reasons()) {
			return xerrors.New("file retention candidate order or reasons do not match reconstructed decision")
		}
	}
	if floorIndex < 0 {
		if classPlan.Floor != nil {
			return xerrors.New("file retention floor exists without verified current-generation inventory")
		}
	} else if classPlan.Floor == nil || classPlan.Floor.Identity != classPlan.Inventory[floorIndex].Identity {
		return xerrors.New("file retention floor does not match reconstructed newest verified point")
	}
	return nil
}

func newestFileRetentionFloorPlan(classPlan apptypes.FileRetentionClassPlan) int {
	selected := -1
	for index, entry := range classPlan.Inventory {
		if !entry.Verified || entry.Generation != classPlan.LiveGeneration || entry.BlockingReason != "" {
			continue
		}
		if selected < 0 || classPlan.Inventory[selected].GenerationCreatedAt < entry.GenerationCreatedAt ||
			(classPlan.Inventory[selected].GenerationCreatedAt == entry.GenerationCreatedAt && classPlan.Inventory[selected].ContentSHA256 < entry.ContentSHA256) ||
			(classPlan.Inventory[selected].GenerationCreatedAt == entry.GenerationCreatedAt && classPlan.Inventory[selected].ContentSHA256 == entry.ContentSHA256 && classPlan.Inventory[selected].RelativePath < entry.RelativePath) {
			selected = index
		}
	}
	return selected
}

func equalFileRetentionStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateFileRetentionClassOrder(classPlan apptypes.FileRetentionClassPlan) error {
	inventoryIDs := make(map[string]apptypes.FileRetentionInventoryPlan, len(classPlan.Inventory))
	for _, entry := range classPlan.Inventory {
		if !lowerHexDigest.MatchString(entry.Identity) || !lowerHexDigest.MatchString(entry.ContentSHA256) || entry.RelativePath == "" {
			return xerrors.New("invalid file retention inventory identity")
		}
		if _, exists := inventoryIDs[entry.Identity]; exists {
			return xerrors.New("duplicate file retention inventory identity")
		}
		inventoryIDs[entry.Identity] = entry
		if entry.MetadataRelativePath != "" {
			if classPlan.Class != "backup" || filepath.Base(entry.MetadataRelativePath) != entry.MetadataRelativePath || !strings.HasPrefix(entry.MetadataRelativePath, apptypes.BackupRetentionManifestPrefix) || !lowerHexDigest.MatchString(entry.MetadataSHA256) {
				return xerrors.New("invalid file retention metadata identity")
			}
		} else if entry.MetadataSHA256 != "" || (classPlan.Class == "backup" && entry.Verified) {
			return xerrors.New("verified backup retention inventory requires exact metadata")
		}
	}
	if classPlan.Status != "satisfied" && (len(classPlan.Candidates) != 0 || len(classPlan.Batches) != 0) {
		return xerrors.New("non-satisfied file retention class must not contain apply batches")
	}
	if len(classPlan.Candidates) != len(classPlan.Batches) {
		return xerrors.New("file retention requires one batch per candidate")
	}
	for index, candidate := range classPlan.Candidates {
		entry, exists := inventoryIDs[candidate.Identity]
		if !exists || entry.RelativePath != candidate.RelativePath || entry.Protected || !entry.Verified || entry.BlockingReason != "" {
			return xerrors.New("file retention candidate is not eligible inventory")
		}
		if classPlan.Batches[index].Ordinal != strconv.Itoa(index) || classPlan.Batches[index].Identity != candidate.Identity {
			return xerrors.New("file retention batch order does not match candidates")
		}
	}
	if len(classPlan.Inventory) > 0 && classPlan.Floor == nil && classPlan.Status == "satisfied" {
		return xerrors.New("non-empty file retention class requires a verified floor")
	}
	if classPlan.Floor != nil {
		entry, exists := inventoryIDs[classPlan.Floor.Identity]
		if !exists || !entry.Protected || !entry.Verified || entry.Generation != classPlan.LiveGeneration || entry.ContentSHA256 != classPlan.Floor.ContentSHA256 {
			return xerrors.New("file retention floor does not match protected inventory")
		}
	}
	return nil
}

func normalizeFileRetentionPlan(plan *apptypes.FileRetentionPlan) {
	sort.Slice(plan.CanonicalPayload.Classes, func(i, j int) bool {
		return plan.CanonicalPayload.Classes[i].Class < plan.CanonicalPayload.Classes[j].Class
	})
	for classIndex := range plan.CanonicalPayload.Classes {
		classPlan := &plan.CanonicalPayload.Classes[classIndex]
		sort.Slice(classPlan.Inventory, func(i, j int) bool {
			left, right := classPlan.Inventory[i], classPlan.Inventory[j]
			if left.GenerationCreatedAt != right.GenerationCreatedAt {
				return left.GenerationCreatedAt < right.GenerationCreatedAt
			}
			if left.Generation != right.Generation {
				return left.Generation < right.Generation
			}
			if left.ContentSHA256 != right.ContentSHA256 {
				return left.ContentSHA256 < right.ContentSHA256
			}
			return left.RelativePath < right.RelativePath
		})
		for index := range classPlan.Candidates {
			reasons := classPlan.Candidates[index].Reasons
			sort.SliceStable(reasons, func(i, j int) bool {
				return fileRetentionReasonOrder(reasons[i]) < fileRetentionReasonOrder(reasons[j])
			})
		}
		classPlan.Batches = make([]apptypes.FileRetentionBatchPlan, len(classPlan.Candidates))
		for index, candidate := range classPlan.Candidates {
			classPlan.Batches[index] = apptypes.FileRetentionBatchPlan{Ordinal: strconv.Itoa(index), Identity: candidate.Identity}
		}
	}
}

func fileRetentionReasonOrder(reason string) int {
	switch reason {
	case "age":
		return 0
	case "count":
		return 1
	case "allocated_bytes":
		return 2
	default:
		return 100
	}
}
