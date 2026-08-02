//nolint:revive,wrapcheck // Public workflow names are explicit; adapter errors retain their typed identity.
package usecase

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
)

const MaxSearchParityEvidenceBytes = 1 << 20

type SearchMaintenanceStore interface {
	AdoptSearchRetirementTarget(context.Context) (apptypes.SearchMaintenanceReport, error)
	SearchRetirementSnapshot(context.Context) (apptypes.SearchRetirementSnapshot, error)
	BeginSearchRetirement(context.Context, apptypes.SearchParityV2Evidence, apptypes.SearchRetirementSnapshot) (apptypes.SearchMaintenanceReport, error)
	RetireLegacySearchBatch(context.Context, int) (apptypes.SearchMaintenanceReport, error)
	BeginSearchRestore(context.Context) (apptypes.SearchMaintenanceReport, error)
	RestoreLegacySearchBatch(context.Context, int) (apptypes.SearchMaintenanceReport, error)
	SearchMaintenanceStatus(context.Context) (apptypes.SearchMaintenanceReport, error)
}

type SearchMaintenanceUsecase struct{ store SearchMaintenanceStore }

func NewSearchMaintenanceUsecase(store SearchMaintenanceStore) *SearchMaintenanceUsecase {
	return &SearchMaintenanceUsecase{store: store}
}

func (u *SearchMaintenanceUsecase) AdoptTarget(ctx context.Context) (apptypes.SearchMaintenanceReport, error) {
	return u.store.AdoptSearchRetirementTarget(ctx)
}

// StartRetire authorizes the irreversible authority switch from a freshly read
// store snapshot. v1, copied-store, stale, dirty, partial, or malformed evidence
// fails closed before the repository is asked to mutate state.
func (u *SearchMaintenanceUsecase) StartRetire(ctx context.Context, artifact []byte, expectedCommit string) (apptypes.SearchMaintenanceReport, error) {
	evidence, err := parseSearchParityV2Evidence(artifact)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	if evidence.Revision.Commit != expectedCommit || evidence.Revision.Dirty {
		return apptypes.SearchMaintenanceReport{}, xerrors.New("parity evidence revision does not authorize retirement")
	}
	snapshot, err := u.store.SearchRetirementSnapshot(ctx)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, xerrors.Errorf("read fresh retirement snapshot: %w", err)
	}
	if snapshot.State.Authority() != model.SearchAuthorityLegacy || snapshot.State.Phase() != model.SearchMaintenanceActive {
		return apptypes.SearchMaintenanceReport{}, xerrors.New("search retirement requires legacy/active state")
	}
	if !snapshot.TargetAdopted || evidence.AuthorizationScope != "actual_target" {
		return apptypes.SearchMaintenanceReport{}, xerrors.New("copied-store compatibility evidence cannot authorize retirement")
	}
	if snapshot.ProjectionState != "complete" || snapshot.ProjectionHighWater != snapshot.SourceHighWater || snapshot.ProjectionRevision != evidence.Projection.Revision || snapshot.ProjectionHighWater != evidence.Projection.HighWater {
		return apptypes.SearchMaintenanceReport{}, xerrors.New("search projection is incomplete, stale, or differs from parity evidence")
	}
	currentProjection := apptypes.SearchParityProjection{Revision: snapshot.ProjectionRevision, HighWater: snapshot.ProjectionHighWater, LogicalBytes: snapshot.CanonicalLogicalBytes, PhysicalBytes: snapshot.PhysicalBytes}
	want, err := apptypes.KeyedSearchParityBinding(snapshot.CursorKey, "target-store", apptypes.SearchParityTargetFields(evidence.Revision, currentProjection, snapshot.EventCount, snapshot.AuditCount, snapshot.CanonicalLogicalBytes, snapshot.ProjectionGeneration, snapshot.ProjectionGeneration)...)
	if err != nil {
		return apptypes.SearchMaintenanceReport{}, err
	}
	got, decodeErr := base64.RawURLEncoding.DecodeString(evidence.TargetStoreBinding)
	wantBytes, wantErr := base64.RawURLEncoding.DecodeString(want)
	if decodeErr != nil || wantErr != nil || !hmac.Equal(got, wantBytes) {
		return apptypes.SearchMaintenanceReport{}, xerrors.New("parity evidence is not bound to the current store")
	}
	if evidence.LiteralGeneration != snapshot.ProjectionGeneration || evidence.BoundedGeneration != snapshot.ProjectionGeneration {
		return apptypes.SearchMaintenanceReport{}, xerrors.New("parity evidence does not bind the active projection generation")
	}
	for _, criterion := range evidence.Criteria {
		expected, bindErr := apptypes.KeyedSearchParityBinding(snapshot.CursorKey, "criterion", apptypes.SearchParityCriterionFields(evidence, criterion)...)
		if bindErr != nil {
			return apptypes.SearchMaintenanceReport{}, bindErr
		}
		expectedBytes, _ := base64.RawURLEncoding.DecodeString(expected)
		observedBytes, observedErr := base64.RawURLEncoding.DecodeString(criterion.CriterionBinding)
		if observedErr != nil || !hmac.Equal(observedBytes, expectedBytes) {
			return apptypes.SearchMaintenanceReport{}, xerrors.New("parity criterion is not bound to the current store suite")
		}
		if criterion.LegacyLatencyUS <= 0 || criterion.TieredLatencyUS <= 0 || criterion.TieredLatencyUS > criterion.LegacyLatencyUS*2+5_000 {
			return apptypes.SearchMaintenanceReport{}, xerrors.New("tiered recent-search latency materially regressed")
		}
	}
	return u.store.BeginSearchRetirement(ctx, evidence, snapshot)
}

func (u *SearchMaintenanceUsecase) ResumeRetire(ctx context.Context, rows int) (apptypes.SearchMaintenanceReport, error) {
	if rows <= 0 || rows > 10_000 {
		return apptypes.SearchMaintenanceReport{}, xerrors.New("retirement rows must be between 1 and 10000")
	}
	return u.store.RetireLegacySearchBatch(ctx, rows)
}
func (u *SearchMaintenanceUsecase) StartRestore(ctx context.Context) (apptypes.SearchMaintenanceReport, error) {
	return u.store.BeginSearchRestore(ctx)
}
func (u *SearchMaintenanceUsecase) ResumeRestore(ctx context.Context, rows int) (apptypes.SearchMaintenanceReport, error) {
	if rows <= 0 || rows > 10_000 {
		return apptypes.SearchMaintenanceReport{}, xerrors.New("restore rows must be between 1 and 10000")
	}
	return u.store.RestoreLegacySearchBatch(ctx, rows)
}
func (u *SearchMaintenanceUsecase) Inspect(ctx context.Context) (apptypes.SearchMaintenanceReport, error) {
	return u.store.SearchMaintenanceStatus(ctx)
}

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func parseSearchParityV2Evidence(data []byte) (apptypes.SearchParityV2Evidence, error) {
	if len(data) == 0 || len(data) > MaxSearchParityEvidenceBytes {
		return apptypes.SearchParityV2Evidence{}, xerrors.New("invalid parity v2 evidence size")
	}
	if err := validateSearchEvidenceObjectKeys(data); err != nil {
		return apptypes.SearchParityV2Evidence{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var evidence apptypes.SearchParityV2Evidence
	if err := dec.Decode(&evidence); err != nil {
		return evidence, xerrors.New("invalid parity v2 evidence")
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return evidence, xerrors.New("invalid trailing parity evidence")
	}
	if evidence.SchemaVersion != apptypes.SearchParityV2Schema || evidence.AuthorizationScope != "actual_target" || !commitPattern.MatchString(evidence.Revision.Commit) || evidence.Revision.Dirty || len(evidence.Criteria) != 2 || evidence.LiteralGeneration == "" || evidence.BoundedGeneration == "" || evidence.RunID == "" || evidence.ComparisonContract == "" {
		return evidence, xerrors.New("parity v2 evidence is not authorizing")
	}
	required := map[string]bool{"fingerprint_eligible": false, "bounded_verification": false}
	for _, criterion := range evidence.Criteria {
		seen, ok := required[criterion.QueryClass]
		binding, decodeErr := base64.RawURLEncoding.DecodeString(criterion.CriterionBinding)
		if !ok || seen || decodeErr != nil || len(binding) != 32 || criterion.Status != "passed" || !criterion.ComparisonEqual || !criterion.CoverageComplete || criterion.LegacyLatencyUS <= 0 || criterion.TieredLatencyUS <= 0 {
			return evidence, xerrors.New("parity v2 criterion is incomplete")
		}
		required[criterion.QueryClass] = true
	}
	return evidence, nil
}

func validateSearchEvidenceObjectKeys(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return xerrors.New("invalid parity v2 evidence")
	}
	if !exactKeys(raw, "schema_version", "authorization_scope", "target_store_binding", "revision", "projection", "literal_generation", "bounded_generation", "run_id", "comparison_contract", "criteria") {
		return xerrors.New("invalid parity v2 evidence keys")
	}
	var revision map[string]json.RawMessage
	if json.Unmarshal(raw["revision"], &revision) != nil || !exactKeys(revision, "commit", "dirty") {
		return xerrors.New("invalid parity revision keys")
	}
	var projection map[string]json.RawMessage
	if json.Unmarshal(raw["projection"], &projection) != nil || !exactKeys(projection, "revision", "high_water", "logical_bytes", "physical_bytes") {
		return xerrors.New("invalid parity projection keys")
	}
	var criteria []map[string]json.RawMessage
	if json.Unmarshal(raw["criteria"], &criteria) != nil {
		return xerrors.New("invalid parity criteria")
	}
	for _, criterion := range criteria {
		if !exactKeys(criterion, "query_class", "criterion_binding", "status", "comparison_equal", "coverage_complete", "legacy_latency_us", "tiered_latency_us") {
			return xerrors.New("invalid parity criterion keys")
		}
	}
	return nil
}
func exactKeys(object map[string]json.RawMessage, keys ...string) bool {
	if len(object) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			return false
		}
	}
	return true
}
