package usecase_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

func TestRecentAgeBindingOnScratchCorpora(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	age := 30 * 24 * time.Hour
	ageCutoff := now.Add(-age)
	const nano = "2006-01-02T15:04:05.000000000Z"
	tests := []struct {
		name        string
		byteCutoff  string
		wantBinding string
		docs        []struct {
			id      string
			created string
			retain  bool
		}
	}{
		{
			name:        "dense ingest: byte cutoff is newer than 30d age",
			byteCutoff:  "2026-06-20T00:00:00.000000000Z",
			wantBinding: "byte",
			docs: []struct {
				id, created string
				retain      bool
			}{
				{"inside-both", "2026-06-25T00:00:00.000000000Z", true},
				{"age-only", "2026-06-10T00:00:00.000000000Z", false},
				{"outside-both", "2026-05-20T00:00:00.000000000Z", false},
			},
		},
		{
			name:        "quiet store: walk never crosses the byte ceiling",
			byteCutoff:  "",
			wantBinding: "age",
			docs: []struct {
				id, created string
				retain      bool
			}{
				{"inside-age", "2026-06-15T00:00:00.000000000Z", true},
				{"outside-age", "2026-05-20T00:00:00.000000000Z", false},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotCutoff := usecase.RecentRetentionCutoff(now, age, tt.byteCutoff)
			byteTime, byteErr := time.Parse(time.RFC3339Nano, tt.byteCutoff)
			switch tt.wantBinding {
			case "byte":
				if byteErr != nil {
					t.Fatal(byteErr)
				}
				if !gotCutoff.Equal(byteTime) {
					t.Fatalf("cutoff=%s, want byte %s", gotCutoff.Format(nano), byteTime.Format(nano))
				}
			case "age":
				if !gotCutoff.Equal(ageCutoff) {
					t.Fatalf("cutoff=%s, want age %s", gotCutoff.Format(nano), ageCutoff.Format(nano))
				}
			default:
				t.Fatalf("unknown binding %q", tt.wantBinding)
			}
			s := apptypes.ProjectionSnapshot{
				Generation:               apptypes.SearchProjectionGeneration{GenerationID: "g"},
				Phase:                    "source",
				Now:                      now,
				RecentCutoffNorm:         tt.byteCutoff,
				RecentSourceCeilingBytes: 1 << 20,
			}
			b := apptypes.SearchProjectionBudget{
				Rows: 16, WallTime: time.Second, LockTime: time.Second,
				StoredBytes: 10_000, DecodedBytes: 10_000, WriteBytes: 10_000,
				RecentAge: age, IndexFamilyBytes: 64 << 20,
			}
			for i, d := range tt.docs {
				s.Documents = append(s.Documents, apptypes.ProjectionDocument{
					Sequence: int64(i + 1), EventID: d.id, SessionID: "s",
					CreatedAt: d.created, Text: "body", StoredBytes: 4, DecodedBytes: 4,
				})
			}
			plan, err := usecase.PlanProjectionBatch(s, b)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Writes) != len(tt.docs) {
				t.Fatalf("writes=%d, want %d", len(plan.Writes), len(tt.docs))
			}
			got := map[string]bool{}
			for _, w := range plan.Writes {
				got[w.Document.EventID] = w.RetainRecent
			}
			want := map[string]bool{}
			for _, d := range tt.docs {
				want[d.id] = d.retain
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("RetainRecent (-want +got):\n%s", diff)
			}
		})
	}
}
