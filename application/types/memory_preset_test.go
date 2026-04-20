package types_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestMemoryRetrievalPresetOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    apptypes.MemoryRetrievalPreset
		wantErr bool
	}{
		{"empty", "", "", false},
		{"whitespace", "   ", "", false},
		{"resume", "resume", apptypes.MemoryRetrievalPresetResume, false},
		{"review", "review", apptypes.MemoryRetrievalPresetReview, false},
		{"incident", "incident", apptypes.MemoryRetrievalPresetIncident, false},
		{"unknown", "nonsense", "", true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := apptypes.MemoryRetrievalPresetOf(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("MemoryRetrievalPresetOf(%q) err = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("MemoryRetrievalPresetOf(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMemoryRetrievalPreset_ApplyToMemoryListCriteriaBuilder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		preset          apptypes.MemoryRetrievalPreset
		wantStatuses    []domtypes.MemoryStatus
		wantMemoryTypes []domtypes.MemoryType
	}{
		{
			name:         "resume keeps type axis open",
			preset:       apptypes.MemoryRetrievalPresetResume,
			wantStatuses: []domtypes.MemoryStatus{domtypes.MemoryStatusAccepted},
		},
		{
			name:            "review narrows to decisions and constraints",
			preset:          apptypes.MemoryRetrievalPresetReview,
			wantStatuses:    []domtypes.MemoryStatus{domtypes.MemoryStatusAccepted},
			wantMemoryTypes: []domtypes.MemoryType{domtypes.MemoryTypeDecision, domtypes.MemoryTypeConstraint},
		},
		{
			name:         "incident adds lessons to the review set",
			preset:       apptypes.MemoryRetrievalPresetIncident,
			wantStatuses: []domtypes.MemoryStatus{domtypes.MemoryStatusAccepted},
			wantMemoryTypes: []domtypes.MemoryType{
				domtypes.MemoryTypeDecision,
				domtypes.MemoryTypeConstraint,
				domtypes.MemoryTypeLesson,
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			builder := apptypes.NewMemoryListCriteriaBuilder(10)
			criteria := tt.preset.ApplyToMemoryListCriteriaBuilder(builder).Build()
			if diff := cmp.Diff(tt.wantStatuses, criteria.Statuses(), cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("Statuses mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantMemoryTypes, criteria.MemoryTypes(), cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("MemoryTypes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestMemoryRetrievalPreset_ExplicitFiltersOverride documents that a
// preset is a *starting point*: if the caller sets Statuses /
// MemoryTypes after applying the preset, the explicit values replace
// the preset's defaults. This is what makes `--preset review --type
// lesson` meaningful — the user wants to look at lessons, not the
// preset's default decision+constraint pair.
func TestMemoryRetrievalPreset_ExplicitFiltersOverride(t *testing.T) {
	t.Parallel()

	builder := apptypes.NewMemoryListCriteriaBuilder(10)
	builder = apptypes.MemoryRetrievalPresetReview.ApplyToMemoryListCriteriaBuilder(builder)
	builder = builder.MemoryTypes([]domtypes.MemoryType{domtypes.MemoryTypeLesson})
	criteria := builder.Build()
	want := []domtypes.MemoryType{domtypes.MemoryTypeLesson}
	if diff := cmp.Diff(want, criteria.MemoryTypes()); diff != "" {
		t.Fatalf("MemoryTypes mismatch (-want +got):\n%s", diff)
	}
}
