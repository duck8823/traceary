package usecase

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain"
)

type coverCaptureBuilder struct {
	faultBuilder
	filter    application.CompactFilter
	set       bool
	gate      application.BodyGate
	report    application.CompactStep
	reportSet bool
}

func (b *coverCaptureBuilder) SetCompactFilter(filter application.CompactFilter) {
	b.set = true
	b.filter = filter
}

func (b *coverCaptureBuilder) InspectBodyGate(context.Context, string, time.Time) (application.BodyGate, error) {
	return b.gate, nil
}

func (b *coverCaptureBuilder) Build(context.Context, string, string) error {
	if b.reportSet {
		b.filter.Report(b.report)
	}
	return nil
}

func dummyCompactSource(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/store.db"
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCompactAttachesCoverWithoutForce(t *testing.T) {
	source := dummyCompactSource(t)
	builder := &coverCaptureBuilder{gate: application.BodyGate{UnrefinedSessions: 3}}
	svc := NewStoreCompactionUsecase(source, &faultJournal{run: compactionRunAt(domain.CompactionCommitted)}, builder, faultFiles{}, faultLease{})
	BindCompactionWorkCover(svc, func(context.Context, string, time.Time) (application.CoverReport, error) {
		return application.CoverReport{RefinementsProduced: 1}, nil
	})
	if _, err := svc.Compact(context.Background(), application.CompactInput{Source: source}); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if builder.filter.AfterClone == nil {
		t.Fatal("AfterClone = nil, want mechanical cover attached")
	}
}

func TestCompactRefuseUnrefinedReturnsTypedError(t *testing.T) {
	source := dummyCompactSource(t)
	builder := &coverCaptureBuilder{gate: application.BodyGate{UnrefinedSessions: 3, UnrefinedBytes: 9}}
	svc := NewStoreCompactionUsecase(source, &faultJournal{run: compactionRunAt(domain.CompactionCommitted)}, builder, faultFiles{}, faultLease{})
	BindCompactionWorkCover(svc, func(context.Context, string, time.Time) (application.CoverReport, error) {
		return application.CoverReport{}, nil
	})
	_, err := svc.Compact(context.Background(), application.CompactInput{Source: source, RefuseUnrefined: true})
	if err == nil {
		t.Fatal("Compact() error = nil, want UnrefinedMaterialError")
	}
	var unrefined application.UnrefinedMaterialError
	if !errors.As(err, &unrefined) {
		t.Fatalf("Compact() error = %v, want UnrefinedMaterialError", err)
	}
	if unrefined.Sessions != 3 {
		t.Fatalf("Sessions = %d, want 3", unrefined.Sessions)
	}
	if builder.set {
		t.Fatal("SetCompactFilter ran, want policy stop before the filter is installed")
	}
}

func TestCompactRefuseUnrefinedSuppressesCoverOnPartialFold(t *testing.T) {
	source := dummyCompactSource(t)
	builder := &coverCaptureBuilder{gate: application.BodyGate{UnrefinedSessions: 3, CoveredCount: 2, CoveredBytes: 4}}
	svc := NewStoreCompactionUsecase(source, &faultJournal{run: compactionRunAt(domain.CompactionCommitted)}, builder, faultFiles{}, faultLease{})
	BindCompactionWorkCover(svc, func(context.Context, string, time.Time) (application.CoverReport, error) {
		return application.CoverReport{RefinementsProduced: 9}, nil
	})
	if _, err := svc.Compact(context.Background(), application.CompactInput{Source: source, RefuseUnrefined: true}); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if builder.filter.AfterClone != nil {
		t.Fatal("AfterClone != nil, want cover suppressed on --refuse-unrefined")
	}
}

func TestCompactWithoutCoverFailsWhenUnrefinedExists(t *testing.T) {
	source := dummyCompactSource(t)
	builder := &coverCaptureBuilder{gate: application.BodyGate{UnrefinedSessions: 3}}
	svc := NewStoreCompactionUsecase(source, &faultJournal{run: compactionRunAt(domain.CompactionCommitted)}, builder, faultFiles{}, faultLease{})
	_, err := svc.Compact(context.Background(), application.CompactInput{Source: source})
	if err == nil {
		t.Fatal("Compact() error = nil, want unbound cover")
	}
	if !strings.Contains(err.Error(), "3 unrefined session") || !strings.Contains(err.Error(), "none is bound") {
		t.Fatalf("Compact() error = %v, want unbound cover naming the unrefined count", err)
	}
}

func TestCompactResultReportsCoveredSessionsFromStep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		step *application.CompactStep
		gate application.BodyGate
		want application.CompactResult
	}{
		{
			name: "step present",
			step: &application.CompactStep{
				Name:       application.CompactStepMechanicalCover,
				Rows:       2,
				BytesAfter: 11,
				Detail:     map[string]int64{"sessions_after": 0, "discarded_body_bytes": 4096},
			},
			gate: application.BodyGate{UnrefinedSessions: 2, UnrefinedBytes: 99, CoveredBytes: 1},
			want: application.CompactResult{
				CoveredSessions:     2,
				UnrefinedRemaining:  0,
				UnrefinedBytes:      11,
				DiscardedBodyBytes:  4096,
				MechanicalSummaries: true,
			},
		},
		{
			name: "step absent",
			gate: application.BodyGate{UnrefinedSessions: 0, UnrefinedBytes: 0, CoveredBytes: 2048},
			want: application.CompactResult{
				CoveredSessions:     0,
				UnrefinedRemaining:  0,
				UnrefinedBytes:      0,
				DiscardedBodyBytes:  2048,
				MechanicalSummaries: false,
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			source := dummyCompactSource(t)
			builder := &coverCaptureBuilder{gate: tc.gate}
			if tc.step != nil {
				builder.report = *tc.step
				builder.reportSet = true
			}
			svc := NewStoreCompactionUsecase(source, &faultJournal{run: compactionRunAt(domain.CompactionCommitted)}, builder, faultFiles{}, faultLease{})
			if tc.gate.NeedsCover() {
				BindCompactionWorkCover(svc, func(context.Context, string, time.Time) (application.CoverReport, error) {
					return application.CoverReport{}, nil
				})
			}
			got, err := svc.Compact(context.Background(), application.CompactInput{Source: source})
			if err != nil {
				t.Fatalf("Compact() error = %v", err)
			}
			gotFields := application.CompactResult{
				CoveredSessions:     got.CoveredSessions,
				UnrefinedRemaining:  got.UnrefinedRemaining,
				UnrefinedBytes:      got.UnrefinedBytes,
				DiscardedBodyBytes:  got.DiscardedBodyBytes,
				MechanicalSummaries: got.MechanicalSummaries,
			}
			if diff := cmp.Diff(tc.want, gotFields); diff != "" {
				t.Fatalf("cover result mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
