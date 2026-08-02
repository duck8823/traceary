package usecase

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
)

type searchMaintenanceStoreFake struct {
	snapshot apptypes.SearchRetirementSnapshot
	began    int
}

func (*searchMaintenanceStoreFake) AdoptSearchRetirementTarget(context.Context) (apptypes.SearchMaintenanceReport, error) {
	return apptypes.SearchMaintenanceReport{}, nil
}

func (f *searchMaintenanceStoreFake) SearchRetirementSnapshot(context.Context) (apptypes.SearchRetirementSnapshot, error) {
	return f.snapshot, nil
}
func (f *searchMaintenanceStoreFake) BeginSearchRetirement(context.Context, string, apptypes.SearchRetirementSnapshot) (apptypes.SearchMaintenanceReport, error) {
	f.began++
	return apptypes.SearchMaintenanceReport{}, nil
}
func (*searchMaintenanceStoreFake) RetireLegacySearchBatch(context.Context, int) (apptypes.SearchMaintenanceReport, error) {
	return apptypes.SearchMaintenanceReport{}, nil
}
func (*searchMaintenanceStoreFake) BeginSearchRestore(context.Context) (apptypes.SearchMaintenanceReport, error) {
	return apptypes.SearchMaintenanceReport{}, nil
}
func (*searchMaintenanceStoreFake) RestoreLegacySearchBatch(context.Context, int) (apptypes.SearchMaintenanceReport, error) {
	return apptypes.SearchMaintenanceReport{}, nil
}
func (*searchMaintenanceStoreFake) SearchMaintenanceStatus(context.Context) (apptypes.SearchMaintenanceReport, error) {
	return apptypes.SearchMaintenanceReport{}, nil
}

func TestSearchMaintenanceStartRetireRequiresFreshStoreBoundV2(t *testing.T) {
	state, err := model.SearchMaintenanceOf(model.SearchAuthorityLegacy, model.SearchMaintenanceActive, 0)
	if err != nil {
		t.Fatal(err)
	}
	revision := apptypes.SearchParityRevision{Commit: strings.Repeat("a", 40)}
	projection := apptypes.SearchParityProjection{Revision: 7, HighWater: 11, LogicalBytes: 99, PhysicalBytes: 202}
	key := []byte("0123456789abcdef0123456789abcdef")
	binding, err := apptypes.KeyedSearchParityBinding(key, "target-store", apptypes.SearchParityTargetFields(revision, projection, 3, 2, 99)...)
	if err != nil {
		t.Fatal(err)
	}
	criterionA, _ := apptypes.KeyedSearchParityBinding(key, "criterion", "fingerprint_eligible", binding)
	criterionB, _ := apptypes.KeyedSearchParityBinding(key, "criterion", "bounded_verification", binding)
	evidence := apptypes.SearchParityV2Evidence{SchemaVersion: apptypes.SearchParityV2Schema, AuthorizationScope: "actual_target", TargetStoreBinding: binding, Revision: revision, Projection: projection, Criteria: []apptypes.SearchParityCriterion{{QueryClass: "fingerprint_eligible", CriterionBinding: criterionA, Status: "passed", ComparisonEqual: true, CoverageComplete: true}, {QueryClass: "bounded_verification", CriterionBinding: criterionB, Status: "passed", ComparisonEqual: true, CoverageComplete: true}}}
	data, _ := json.Marshal(evidence)
	fake := &searchMaintenanceStoreFake{snapshot: apptypes.SearchRetirementSnapshot{State: state, CursorKey: key, ProjectionRevision: 7, ProjectionHighWater: 11, SourceHighWater: 11, ProjectionState: "complete", EventCount: 3, AuditCount: 2, CanonicalLogicalBytes: 99, PhysicalBytes: 202, TargetAdopted: true}}
	if _, err = NewSearchMaintenanceUsecase(fake).StartRetire(context.Background(), data, revision.Commit); err != nil {
		t.Fatalf("StartRetire() error=%v", err)
	}
	if fake.began != 1 {
		t.Fatalf("begin calls=%d", fake.began)
	}
	for name, mutate := range map[string]func(*apptypes.SearchParityV2Evidence){"v1": func(e *apptypes.SearchParityV2Evidence) { e.SchemaVersion = "traceary.tiered-search-parity/v1" }, "copied key": func(e *apptypes.SearchParityV2Evidence) { e.TargetStoreBinding = criterionA }, "copied scope": func(e *apptypes.SearchParityV2Evidence) { e.AuthorizationScope = "compatibility_only" }, "partial": func(e *apptypes.SearchParityV2Evidence) { e.Criteria[0].CoverageComplete = false }, "dirty": func(e *apptypes.SearchParityV2Evidence) { e.Revision.Dirty = true }} {
		t.Run(name, func(t *testing.T) {
			crafted := evidence
			crafted.Criteria = append([]apptypes.SearchParityCriterion(nil), evidence.Criteria...)
			mutate(&crafted)
			encoded, _ := json.Marshal(crafted)
			fresh := *fake
			fresh.began = 0
			if _, err := NewSearchMaintenanceUsecase(&fresh).StartRetire(context.Background(), encoded, revision.Commit); err == nil {
				t.Fatal("non-authorizing evidence accepted")
			}
			if fresh.began != 0 {
				t.Fatal("mutation began")
			}
		})
	}
}

func TestSearchMaintenanceEvidenceRejectsCaseAliasesAndOversize(t *testing.T) {
	alias := []byte(`{"schema_version":"traceary.tiered-search-parity/v2","SCHEMA_VERSION":"traceary.tiered-search-parity/v2","authorization_scope":"actual_target","target_store_binding":"x","revision":{},"projection":{},"criteria":[]}`)
	if _, err := parseSearchParityV2Evidence(alias); err == nil {
		t.Fatal("case alias accepted")
	}
	if _, err := parseSearchParityV2Evidence(make([]byte, MaxSearchParityEvidenceBytes+1)); err == nil {
		t.Fatal("oversize accepted")
	}
}
