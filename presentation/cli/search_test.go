package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_SearchTieredPreviewExposesZeroMatchContinuation(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	stub := &cliTieredSearchStub{page: apptypes.LiteralSearchPage{Tier: apptypes.LiteralSearchTierBoundedVerification, Coverage: apptypes.LiteralSearchCoverage{ProcessedSources: 4, HighWater: 9}, PartialReason: "source_rows", Continuation: "next"}}
	stdout := &bytes.Buffer{}
	root := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{}), cli.WithEvent(&eventUsecaseStub{}), cli.WithTieredEventSearch(stub)).Command()
	root.SetOut(stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"search", "needle", "--tiered-preview", "--deep", "--continuation", "previous", "--json", "--workspace", "repo"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); !strings.Contains(got, `"tier":"bounded_verification"`) || !strings.Contains(got, `"continuation":"next"`) || !strings.Contains(got, `"partial_reason":"source_rows"`) {
		t.Fatalf("stdout = %s", got)
	}
	if stub.request.Continuation != "previous" || stub.request.Budget != apptypes.DeepLiteralSearchBudget {
		t.Fatalf("request = %+v", stub.request)
	}
}

func TestRootCLI_SearchTieredPreviewResumesWithoutImplicitTo(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	eventUC := usecase.NewEventUsecase(eventDS, eventDS)
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"cli-tiered-a", "cli-tiered-b"} {
		eventID, _ := types.EventIDFrom(id)
		agent, _ := types.AgentFrom("codex")
		sessionID, _ := types.SessionIDFrom("cli-tiered-session")
		if err := eventDS.Save(ctx, model.EventOf(eventID, types.EventKindNote, types.Client("cli"), agent, sessionID, types.Workspace("repo"), "needle", time.Now().UTC())); err != nil {
			t.Fatal(err)
		}
	}
	search := queryservice.NewLiteralSearchService(eventDS)
	run := func(continuation string) string {
		t.Helper()
		stdout := &bytes.Buffer{}
		root := cli.NewRootCLI(cli.WithStoreManagement(storeUC), cli.WithEvent(eventUC), cli.WithTieredEventSearch(search), cli.WithDatabasePathSetter(db.SetPath)).Command()
		root.SetOut(stdout)
		root.SetErr(&bytes.Buffer{})
		args := []string{"search", "needle", "--tiered-preview", "--json", "--workspace", "repo", "--limit", "1", "--db-path", dbPath}
		if continuation != "" {
			args = append(args, "--continuation", continuation)
		}
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		var output struct {
			Continuation string `json:"continuation"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
			t.Fatal(err)
		}
		return output.Continuation
	}
	continuation := run("")
	if continuation == "" {
		t.Fatal("missing continuation")
	}
	time.Sleep(time.Millisecond)
	_ = run(continuation)
}

type cliTieredSearchStub struct {
	page    apptypes.LiteralSearchPage
	request apptypes.LiteralSearchRequest
}

func (s *cliTieredSearchStub) SearchLiteralPage(_ context.Context, r apptypes.LiteralSearchRequest) (apptypes.LiteralSearchPage, error) {
	s.request = r
	return s.page, nil
}

func TestRootCLI_SearchCommand(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	eventID, err := types.EventIDFrom("event-1")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-1")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{
			searchEvents: []*model.Event{
				model.EventOf(
					eventID,
					types.EventKindNote,
					"cli",
					agent,
					sessionID,
					"github.com/duck8823/traceary",
					"hello traceary",
					time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
				),
			},
		}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--session-id", "session-1",
		"--client", "cli",
		"--agent", "codex",
		"--kind", "note",
		"--from", "2026-04-07",
		"--since", "2026-04-07",
		"--to", "2026-04-07",
		"--until", "2026-04-07",
		"--limit", "5",
		"--offset", "2",
		"traceary",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout.String() == "" {
		t.Fatalf("stdout is empty")
	}
}

func TestRootCLI_SearchCommand_JSON(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	eventID, err := types.EventIDFrom("event-2")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-2")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{
			searchEvents: []*model.Event{
				model.EventOf(
					eventID,
					types.EventKindNote,
					"cli",
					agent,
					sessionID,
					"github.com/duck8823/traceary",
					"hello json search",
					time.Date(2026, 4, 7, 13, 0, 0, 0, time.UTC),
				),
			},
		}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--json",
		"traceary",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	want := "" +
		"[\n" +
		"  {\n" +
		"    \"event_id\": \"event-2\",\n" +
		"    \"kind\": \"note\",\n" +
		"    \"client\": \"cli\",\n" +
		"    \"agent\": \"codex\",\n" +
		"    \"session_id\": \"session-2\",\n" +
		"    \"workspace\": \"github.com/duck8823/traceary\",\n" +
		"    \"message\": \"hello json search\",\n" +
		"    \"created_at\": \"2026-04-07T13:00:00Z\"\n" +
		"  }\n" +
		"]\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRootCLI_SearchCommand_FilterOnly(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--session-id", "session-42",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRootCLI_SearchCommand_NegativeOffset(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--offset", "-1",
		"traceary",
	})

	if err := rootCmd.Execute(); err == nil {
		t.Fatalf("Execute() error = nil, want error")
	}
}

func TestRootCLI_SearchCommand_FailuresOnlyAsConstraint(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"search",
		"--db-path", "/tmp/traceary.db",
		"--failures",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; --failures alone should count as a valid search constraint", err)
	}
}
