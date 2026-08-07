package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/presentation/cli"
)

type sessionRefinementUsecaseStub struct {
	result model.SessionRefineResult
	err    error
	input  usecase.SessionRefineInput
	calls  int
}

func (s *sessionRefinementUsecaseStub) Refine(_ context.Context, input usecase.SessionRefineInput) (model.SessionRefineResult, error) {
	s.calls++
	s.input = input
	if s.err != nil {
		return model.SessionRefineResult{}, s.err
	}
	return s.result, nil
}

func TestRootCLI_SessionRefineCommand_ReportsOutcomes(t *testing.T) {
	t.Parallel()

	producedAt := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	refinement, err := model.NewSessionRefinement(
		"sess-1",
		2,
		"evt-from",
		"evt-to",
		"summary text",
		"kw",
		"agent",
		producedAt,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		outcome     model.SessionRefineOutcome
		asJSON      bool
		wantHuman   string
		wantOutcome string
	}{
		{
			name:        "human created",
			outcome:     model.SessionRefineOutcomeCreated,
			wantHuman:   "Created: session=sess-1 generation=2 covers=evt-from..evt-to",
			wantOutcome: "created",
		},
		{
			name:        "human superseded",
			outcome:     model.SessionRefineOutcomeSuperseded,
			wantHuman:   "Superseded: session=sess-1 generation=2 covers=evt-from..evt-to",
			wantOutcome: "superseded",
		},
		{
			name:        "human unchanged",
			outcome:     model.SessionRefineOutcomeUnchanged,
			wantHuman:   "Unchanged: session=sess-1 generation=2 covers=evt-from..evt-to",
			wantOutcome: "unchanged",
		},
		{
			name:        "json created",
			outcome:     model.SessionRefineOutcomeCreated,
			asJSON:      true,
			wantOutcome: "created",
		},
		{
			name:        "json superseded",
			outcome:     model.SessionRefineOutcomeSuperseded,
			asJSON:      true,
			wantOutcome: "superseded",
		},
		{
			name:        "json unchanged",
			outcome:     model.SessionRefineOutcomeUnchanged,
			asJSON:      true,
			wantOutcome: "unchanged",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := model.SessionRefineResultOf(tt.outcome, refinement)
			if err != nil {
				t.Fatal(err)
			}
			stub := &sessionRefinementUsecaseStub{result: result}
			stdout := &bytes.Buffer{}
			rootCmd := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithSessionRefinement(stub),
			).Command()
			rootCmd.SetOut(stdout)
			rootCmd.SetErr(&bytes.Buffer{})
			args := []string{
				"session", "refine", "sess-1",
				"--summary", "summary text",
				"--covers-to", "evt-to",
				"--keywords", "kw",
				"--produced-by", "agent",
				"--db-path", t.TempDir() + "/traceary.db",
			}
			if tt.asJSON {
				args = append(args, "--json")
			}
			rootCmd.SetArgs(args)

			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if stub.calls != 1 {
				t.Fatalf("Refine calls = %d, want 1", stub.calls)
			}
			if stub.input.SessionID != "sess-1" || stub.input.CoversTo != "evt-to" {
				t.Fatalf("input = %+v", stub.input)
			}
			if tt.asJSON {
				var payload map[string]any
				if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
					t.Fatalf("json.Unmarshal() error = %v\nbody=%s", err, stdout.String())
				}
				if payload["outcome"] != tt.wantOutcome {
					t.Fatalf("outcome = %v, want %s", payload["outcome"], tt.wantOutcome)
				}
				if payload["session_id"] != "sess-1" {
					t.Fatalf("session_id = %v", payload["session_id"])
				}
				if int(payload["generation"].(float64)) != 2 {
					t.Fatalf("generation = %v, want 2", payload["generation"])
				}
				return
			}
			got := strings.TrimSpace(stdout.String())
			if diff := cmp.Diff(tt.wantHuman, got); diff != "" {
				t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRootCLI_SessionRefineCommand_RequiresFlags(t *testing.T) {
	t.Parallel()

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSessionRefinement(&sessionRefinementUsecaseStub{}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "refine", "sess-1", "--db-path", t.TempDir() + "/traceary.db"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want required-flag error")
	}
}
