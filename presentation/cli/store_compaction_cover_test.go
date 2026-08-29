package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
	"github.com/duck8823/traceary/presentation/cli"
)

type compactCoverStub struct {
	in     application.CompactInput
	result application.CompactResult
	err    error
}

func (s *compactCoverStub) Compact(_ context.Context, in application.CompactInput) (application.CompactResult, error) {
	s.in = in
	return s.result, s.err
}
func (*compactCoverStub) Plan(context.Context, string) (domain.CompactionRun, error) {
	return domain.CompactionRun{}, nil
}
func (*compactCoverStub) Apply(context.Context, string) (domain.CompactionRun, error) {
	return domain.CompactionRun{}, nil
}
func (*compactCoverStub) Resume(context.Context, string) (domain.CompactionRun, error) {
	return domain.CompactionRun{}, nil
}
func (*compactCoverStub) Status(context.Context, string) (domain.CompactionRun, error) {
	return domain.CompactionRun{}, nil
}
func (*compactCoverStub) Rollback(context.Context, string) (domain.CompactionRun, error) {
	return domain.CompactionRun{}, nil
}
func (*compactCoverStub) AbandonStalePrePublication(context.Context, string) (domain.CompactionRun, error) {
	return domain.CompactionRun{}, nil
}

func TestStoreCompactRejectsRemovedForceFlag(t *testing.T) {
	t.Parallel()
	root := cli.NewRootCLI(cli.WithStoreCompactionFactory(func(string) application.StoreCompactionUsecase {
		return &compactCoverStub{}
	})).Command()
	root.SetArgs([]string{"store", "compact", "--force"})
	err := root.Execute()
	if err == nil {
		t.Fatal("store compact --force error = nil, want unknown flag")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("error = %v, want unknown flag", err)
	}
}

func TestStoreCompactPassesRefuseUnrefined(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "absent", args: []string{"store", "compact", "--db-path", t.TempDir() + "/store.db"}},
		{name: "set", args: []string{"store", "compact", "--db-path", t.TempDir() + "/store.db", "--refuse-unrefined"}, want: true},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			stub := &compactCoverStub{result: application.CompactResult{Run: domain.CompactionRun{ID: "run", Phase: domain.CompactionCommitted}}}
			root := cli.NewRootCLI(cli.WithStoreCompactionFactory(func(string) application.StoreCompactionUsecase { return stub })).Command()
			root.SetArgs(tc.args)
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if stub.in.RefuseUnrefined != tc.want {
				t.Fatalf("RefuseUnrefined = %v, want %v", stub.in.RefuseUnrefined, tc.want)
			}
		})
	}
}

func TestStoreCompactJSONReportsCoveredSessions(t *testing.T) {
	t.Parallel()
	stub := &compactCoverStub{result: application.CompactResult{
		Run:                 domain.CompactionRun{ID: "run", Phase: domain.CompactionCommitted, RollbackPath: "store.db.rollback-run"},
		CoveredSessions:     2,
		DiscardedBodyBytes:  4096,
		MechanicalSummaries: true,
		CompactStrategy:     application.CompactStrategyReplica,
		Steps: application.CompactSteps{{
			Name: application.CompactStepMechanicalCover,
			Rows: 2,
			Detail: map[string]int64{
				"sessions_after": 0,
			},
		}},
	}}
	root := cli.NewRootCLI(cli.WithStoreCompactionFactory(func(string) application.StoreCompactionUsecase { return stub })).Command()
	path := t.TempDir() + "/store.db"
	root.SetArgs([]string{"store", "compact", "--db-path", path})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", stdout.String(), err)
	}
	if payload["covered_sessions"] != float64(2) {
		t.Fatalf("covered_sessions = %v, want 2", payload["covered_sessions"])
	}
	if payload["discarded_body_bytes"] != float64(4096) {
		t.Fatalf("discarded_body_bytes = %v, want 4096", payload["discarded_body_bytes"])
	}
	if payload["mechanical_summaries"] != true {
		t.Fatalf("mechanical_summaries = %v, want true", payload["mechanical_summaries"])
	}
	steps, _ := payload["steps"].(map[string]any)
	cover, _ := steps["mechanical_cover"].(map[string]any)
	if cover["rows"] != float64(2) {
		t.Fatalf("steps.mechanical_cover.rows = %v, want 2", cover["rows"])
	}
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	wantKeys := []string{
		"bytes_after",
		"bytes_before",
		"compact_strategy",
		"covered_sessions",
		"discarded_body_bytes",
		"estimated_reclaimable_bytes",
		"mechanical_summaries",
		"phase",
		"released_command_body_bytes",
		"released_command_body_rows",
		"rollback_path",
		"rollback_retained",
		"run_id",
		"steps",
		"unrefined_bytes",
		"unrefined_remaining",
	}
	if diff := cmp.Diff(wantKeys, keys); diff != "" {
		t.Fatalf("JSON keys mismatch (-want +got):\n%s", diff)
	}
}

func TestStoreCompactExitsZeroAndCompletesProjectionWhenOpenExceedsWallTime(t *testing.T) {
	t.Parallel()
	if got := apptypes.DefaultSearchProjectionBudget().WallTime; got != time.Second {
		t.Fatalf("DefaultSearchProjectionBudget().WallTime = %s, want 1s", got)
	}
	b := apptypes.DefaultSearchProjectionBudget()
	b.WallTime = time.Millisecond
	store := &slowOpenProjectionStore{budget: b, openDelay: 40 * time.Millisecond}
	committed := application.CompactResult{
		Run:             domain.CompactionRun{ID: "run", Phase: domain.CompactionCommitted},
		CompactStrategy: application.CompactStrategyReplica,
	}
	stub := &compactCoverStub{result: committed}
	root := cli.NewRootCLI(cli.WithStoreCompactionFactory(func(string) application.StoreCompactionUsecase {
		return &compactThenCompleteStub{
			inner: stub,
			complete: func(ctx context.Context) error {
				return usecase.NewSearchProjectionUsecase(store).CompleteGeneration(ctx, b, time.Now())
			},
		}
	})).Command()
	root.SetArgs([]string{"store", "compact", "--db-path", t.TempDir() + "/store.db"})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	if err := root.Execute(); err != nil {
		t.Fatalf("store compact error = %v, want exit 0", err)
	}
	if store.state != "complete" {
		t.Fatalf("projection state = %q, want complete", store.state)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", stdout.String(), err)
	}
	if payload["phase"] != string(domain.CompactionCommitted) && payload["phase"] != "committed" {
		t.Fatalf("phase = %v, want committed", payload["phase"])
	}
}

type compactThenCompleteStub struct {
	inner    *compactCoverStub
	complete func(context.Context) error
}

func (s *compactThenCompleteStub) Compact(ctx context.Context, in application.CompactInput) (application.CompactResult, error) {
	result, err := s.inner.Compact(ctx, in)
	if err != nil {
		return result, err
	}
	if s.complete != nil {
		if completeErr := s.complete(ctx); completeErr != nil {
			return result, completeErr
		}
	}
	return result, nil
}
func (s *compactThenCompleteStub) Plan(ctx context.Context, source string) (domain.CompactionRun, error) {
	return s.inner.Plan(ctx, source)
}
func (s *compactThenCompleteStub) Apply(ctx context.Context, source string) (domain.CompactionRun, error) {
	return s.inner.Apply(ctx, source)
}
func (s *compactThenCompleteStub) Resume(ctx context.Context, source string) (domain.CompactionRun, error) {
	return s.inner.Resume(ctx, source)
}
func (s *compactThenCompleteStub) Status(ctx context.Context, source string) (domain.CompactionRun, error) {
	return s.inner.Status(ctx, source)
}
func (s *compactThenCompleteStub) Rollback(ctx context.Context, source string) (domain.CompactionRun, error) {
	return s.inner.Rollback(ctx, source)
}
func (s *compactThenCompleteStub) AbandonStalePrePublication(ctx context.Context, source string) (domain.CompactionRun, error) {
	return s.inner.AbandonStalePrePublication(ctx, source)
}

type slowOpenProjectionStore struct {
	budget    apptypes.SearchProjectionBudget
	openDelay time.Duration
	state     string
}

func (s *slowOpenProjectionStore) delayOpen(ctx context.Context) error {
	if s.openDelay <= 0 {
		return nil
	}
	openCtx := application.StoreOpenContext(ctx)
	timer := time.NewTimer(s.openDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-openCtx.Done():
		return xerrors.Errorf("store open: %w", openCtx.Err())
	}
}

func (s *slowOpenProjectionStore) Start(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionGeneration, error) {
	s.state = "rebuilding"
	return apptypes.SearchProjectionGeneration{GenerationID: "generation"}, nil
}
func (s *slowOpenProjectionStore) SearchProjectionStatus(context.Context) (apptypes.SearchProjectionStatus, error) {
	return apptypes.SearchProjectionStatus{State: s.state, ConfigHash: s.budget.ConfigHash()}, nil
}
func (s *slowOpenProjectionStore) SearchProjectionControlStatus(context.Context) (apptypes.SearchProjectionControlStatus, error) {
	state := s.state
	if state == "" {
		state = "abandoned"
	}
	return apptypes.SearchProjectionControlStatus{State: state, ConfigHash: s.budget.ConfigHash(), CapacitySemanticsVersion: apptypes.SearchProjectionCapacitySemanticsVersion}, nil
}
func (s *slowOpenProjectionStore) SelectSnapshot(ctx context.Context, _ apptypes.SearchProjectionBudget, now time.Time) (apptypes.ProjectionSnapshot, error) {
	if err := s.delayOpen(ctx); err != nil {
		return apptypes.ProjectionSnapshot{}, err
	}
	return apptypes.ProjectionSnapshot{
		Generation:  apptypes.SearchProjectionGeneration{GenerationID: "generation"},
		Phase:       "cleanup",
		CleanupAll:  true,
		CleanupDone: true,
		Now:         now,
	}, nil
}
func (s *slowOpenProjectionStore) ApplyBatch(ctx context.Context, plan apptypes.ProjectionBatchPlan, _ time.Duration, _ time.Time) (apptypes.SearchProjectionProgress, error) {
	if err := s.delayOpen(ctx); err != nil {
		return apptypes.SearchProjectionProgress{}, err
	}
	return apptypes.SearchProjectionProgress{Completed: plan.Completed, GenerationID: plan.GenerationID}, nil
}
func (s *slowOpenProjectionStore) CleanupBatch(ctx context.Context, plan apptypes.ProjectionBatchPlan, _ time.Duration, _ time.Time) (apptypes.SearchProjectionProgress, error) {
	if err := s.delayOpen(ctx); err != nil {
		return apptypes.SearchProjectionProgress{}, err
	}
	if plan.Completed {
		s.state = "complete"
	}
	return apptypes.SearchProjectionProgress{Completed: plan.Completed, GenerationID: plan.GenerationID}, nil
}
func (*slowOpenProjectionStore) MarkFailed(context.Context, string, int64, string, time.Time) error {
	return nil
}

func TestStoreCompactJSONWhenProjectionCompleteFails(t *testing.T) {
	t.Parallel()
	stub := &compactCoverStub{
		result: application.CompactResult{
			Run:             domain.CompactionRun{ID: "run", Phase: domain.CompactionCommitted},
			CompactStrategy: application.CompactStrategyInPlace,
			Steps: application.CompactSteps{{
				Name:           application.CompactStepAuditEncode,
				Rows:           3,
				BytesReclaimed: 12,
			}},
		},
		err: errors.New("complete search projection after compact: context deadline exceeded"),
	}
	root := cli.NewRootCLI(cli.WithStoreCompactionFactory(func(string) application.StoreCompactionUsecase { return stub })).Command()
	root.SetArgs([]string{"store", "compact", "--db-path", t.TempDir() + "/store.db"})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	err := root.Execute()
	if err == nil {
		t.Fatal("store compact error = nil, want projection complete failure")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("error = %v, want projection deadline", err)
	}
	var payload map[string]any
	if decodeErr := json.Unmarshal(stdout.Bytes(), &payload); decodeErr != nil {
		t.Fatalf("stdout %q is not JSON: %v", stdout.String(), decodeErr)
	}
	if payload["compact_strategy"] != application.CompactStrategyInPlace {
		t.Fatalf("compact_strategy = %v, want %q", payload["compact_strategy"], application.CompactStrategyInPlace)
	}
	steps, _ := payload["steps"].(map[string]any)
	audit, _ := steps["audit_encode"].(map[string]any)
	if audit["rows"] != float64(3) {
		t.Fatalf("steps.audit_encode.rows = %v, want 3", audit["rows"])
	}
}

func TestStoreCompactStderrStatesCoveredSessions(t *testing.T) {
	t.Parallel()
	stub := &compactCoverStub{result: application.CompactResult{
		Run:                domain.CompactionRun{ID: "run", Phase: domain.CompactionCommitted},
		CoveredSessions:    2,
		DiscardedBodyBytes: 4096,
	}}
	root := cli.NewRootCLI(cli.WithStoreCompactionFactory(func(string) application.StoreCompactionUsecase { return stub })).Command()
	root.SetArgs([]string{"store", "compact", "--db-path", t.TempDir() + "/store.db"})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "2") {
		t.Fatalf("stderr %q does not state covered session count", stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout leaked non-JSON: %q (%v)", stdout.String(), err)
	}
}

func TestStoreCompactRefuseUnrefinedExclusiveWithAbsorbFlags(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"--archive", "--retention-plan", "--projection-rebuild"} {
		flag := flag
		t.Run(flag, func(t *testing.T) {
			root := cli.NewRootCLI(cli.WithStoreCompactionFactory(func(string) application.StoreCompactionUsecase {
				return &compactCoverStub{}
			})).Command()
			args := []string{"store", "compact", "--refuse-unrefined", flag}
			if flag == "--archive" {
				args = append(args, "--output", t.TempDir()+"/out")
			}
			root.SetArgs(args)
			err := root.Execute()
			if err == nil {
				t.Fatal("error = nil, want exclusivity")
			}
			if !strings.Contains(err.Error(), "--refuse-unrefined") {
				t.Fatalf("error = %v, want it to name --refuse-unrefined", err)
			}
		})
	}
}
