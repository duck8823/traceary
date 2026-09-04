package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application"
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
	if _, ok := steps["search_projection"]; ok {
		t.Fatal("compact JSON must not include a search_projection step")
	}
}

func TestStoreCompactRejectsRemovedProjectionFlags(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{
		"--projection-rebuild",
		"--projection-abort",
		"--index-family-bytes",
		"--decoded-bytes",
		"--recent-age",
		"--lock-time",
	} {
		flag := flag
		t.Run(flag, func(t *testing.T) {
			root := cli.NewRootCLI(cli.WithStoreCompactionFactory(func(string) application.StoreCompactionUsecase {
				return &compactCoverStub{}
			})).Command()
			root.SetArgs([]string{"store", "compact", flag, "1"})
			err := root.Execute()
			if err == nil {
				t.Fatal("error = nil, want unknown flag")
			}
			if !strings.Contains(err.Error(), "unknown flag") && !strings.Contains(err.Error(), "unknown") {
				t.Fatalf("error = %v, want unknown flag", err)
			}
		})
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
	for _, flag := range []string{"--archive", "--retention-plan"} {
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
