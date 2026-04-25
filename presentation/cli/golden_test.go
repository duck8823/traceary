package cli_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

var updateGolden = flag.Bool("update", false, "update golden test fixtures")

func assertJSONGolden(t *testing.T, got []byte, fixturePath string) {
	t.Helper()
	assertGolden(t, got, fixturePath)
}

func assertNDJSONGolden(t *testing.T, got []byte, fixturePath string) {
	t.Helper()
	assertGolden(t, got, fixturePath)
}

func assertGolden(t *testing.T, got []byte, fixturePath string) {
	t.Helper()

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(fixturePath), 0o755); err != nil {
			t.Fatalf("create golden fixture directory %q: %v", filepath.Dir(fixturePath), err)
		}
		if err := os.WriteFile(fixturePath, got, 0o644); err != nil {
			t.Fatalf("update golden fixture %q: %v", fixturePath, err)
		}
	}

	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read golden fixture %q: %v", fixturePath, err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Fatalf("golden fixture mismatch %q (-want +got):\n%s", fixturePath, diff)
	}
}

func TestGoldenHarness_NDJSONHelper(t *testing.T) {
	fixturePath := filepath.Join(t.TempDir(), "events.ndjson.golden")
	got := []byte("{\"event_id\":\"event-1\"}\n")
	if err := os.WriteFile(fixturePath, got, 0o644); err != nil {
		t.Fatalf("write NDJSON fixture: %v", err)
	}

	assertNDJSONGolden(t, got, fixturePath)
}

func TestEventShow_JSON_Golden(t *testing.T) {
	eventID, err := types.EventIDFrom("event-golden-1")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-golden-1")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	eventDetails, err := apptypes.EventDetailsOf(
		model.EventOf(
			eventID,
			types.EventKindCommandExecuted,
			"cli",
			agent,
			sessionID,
			"duck8823/traceary",
			"go test ./presentation/cli",
			time.Date(2026, 4, 8, 12, 30, 0, 0, time.UTC),
		),
		types.Some(model.CommandAuditOf(
			eventID,
			"go test ./presentation/cli",
			"stdin",
			"stdout",
			false,
			false,
			types.Some(0),
		)),
	)
	if err != nil {
		t.Fatalf("EventDetailsOf() error = %v", err)
	}

	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{showDetails: eventDetails}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"show", "--db-path", "/tmp/test-traceary.db", "--json", "event-golden-1"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	assertJSONGolden(t, stdout.Bytes(), filepath.Join("testdata", "show", "with_audit.golden.json"))
}

func TestCoreReadJSONGoldens(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	t.Setenv("TRACEARY_SESSION_ID", "")

	events := []*model.Event{
		mustGoldenEvent(t, "event-golden-list-1", types.EventKindNote, "cli", "codex", "session-golden-list", "duck8823/traceary", "first golden event", time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC), ""),
		mustGoldenEvent(t, "event-golden-list-2", types.EventKindPrompt, "hook", "codex", "session-golden-list", "duck8823/traceary", "prompt with hook provenance", time.Date(2026, 4, 8, 13, 5, 0, 0, time.UTC), "user_prompt_submit"),
	}

	t.Run("list empty result", func(t *testing.T) {
		stdout := executeGoldenCommand(t, &eventUsecaseStub{}, nil, []string{
			"list", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--json",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "list", "empty.golden.json"))
	})

	t.Run("list multi event", func(t *testing.T) {
		stdout := executeGoldenCommand(t, &eventUsecaseStub{listEvents: events}, nil, []string{
			"list", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--json",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "list", "multi_event.golden.json"))
	})

	t.Run("search empty result", func(t *testing.T) {
		stdout := executeGoldenCommand(t, &eventUsecaseStub{}, nil, []string{
			"search", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--json", "missing",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "search", "empty.golden.json"))
	})

	t.Run("search single event", func(t *testing.T) {
		stdout := executeGoldenCommand(t, &eventUsecaseStub{searchEvents: events[:1]}, nil, []string{
			"search", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--json", "golden",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "search", "single_event.golden.json"))
	})

	t.Run("log single event", func(t *testing.T) {
		logEvent := mustGoldenEvent(t, "event-golden-log-1", types.EventKindTranscript, "cli", "codex", "session-golden-log", "duck8823/traceary", "transcript token=[REDACTED]", time.Date(2026, 4, 8, 14, 0, 0, 0, time.UTC), "")
		stdout := executeGoldenCommand(t, &eventUsecaseStub{logEvent: logEvent}, nil, []string{
			"log", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--session-id", "session-golden-log", "--kind", "transcript", "--json", "transcript token=secret",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "log", "single_event.golden.json"))
	})

	t.Run("audit with redacted payload", func(t *testing.T) {
		eventID, err := types.EventIDFrom("event-golden-audit-1")
		if err != nil {
			t.Fatalf("EventIDFrom() error = %v", err)
		}
		audit := model.CommandAuditOf(
			eventID,
			"curl https://example.test",
			`{"access_token":"[REDACTED]"}`,
			"Authorization: Bearer [REDACTED]",
			false,
			true,
			types.Some(1),
		)
		audit.SetRedaction(true, true)
		auditEvent := mustGoldenEvent(t, eventID.String(), types.EventKindCommandExecuted, "cli", "codex", "session-golden-audit", "duck8823/traceary", "curl https://example.test", time.Date(2026, 4, 8, 14, 30, 0, 0, time.UTC), "")
		stdout := executeGoldenCommand(t, &eventUsecaseStub{auditEvent: auditEvent, auditAudit: audit}, nil, []string{
			"audit", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--session-id", "session-golden-audit", "--exit-code", "1", "--json",
			"curl https://example.test", `{"access_token":"top-secret"}`, "Authorization: Bearer token-value",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "audit", "redacted_with_audit.golden.json"))
	})

	t.Run("context empty result", func(t *testing.T) {
		stdout := executeGoldenCommand(t, &eventUsecaseStub{}, nil, []string{
			"context", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--session-id", "session-golden-context", "--json",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "context", "empty.golden.json"))
	})

	t.Run("context multi event", func(t *testing.T) {
		contextEvents := []*model.Event{
			mustGoldenEvent(t, "event-golden-context-1", types.EventKindSessionStarted, "cli", "codex", "session-golden-context", "duck8823/traceary", "session started", time.Date(2026, 4, 8, 15, 0, 0, 0, time.UTC), ""),
			mustGoldenEvent(t, "event-golden-context-2", types.EventKindNote, "cli", "codex", "session-golden-context", "duck8823/traceary", "next: verify goldens", time.Date(2026, 4, 8, 15, 5, 0, 0, time.UTC), "stop"),
		}
		stdout := executeGoldenCommand(t, &eventUsecaseStub{contextEvents: contextEvents}, nil, []string{
			"context", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--session-id", "session-golden-context", "--json",
		})

		assertJSONGolden(t, stdout, filepath.Join("testdata", "context", "multi_event.golden.json"))
	})
}

func executeGoldenCommand(t *testing.T, eventStub *eventUsecaseStub, sessionStub *sessionUsecaseStub, args []string) []byte {
	t.Helper()

	options := []cli.RootCLIOption{
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
	}
	if sessionStub != nil {
		options = append(options, cli.WithSession(sessionStub))
	}

	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(options...).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(args)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(%v) error = %v", args, err)
	}

	return stdout.Bytes()
}

func mustGoldenEvent(
	t *testing.T,
	eventIDValue string,
	kind types.EventKind,
	client string,
	agentValue string,
	sessionIDValue string,
	workspace string,
	body string,
	createdAt time.Time,
	sourceHook string,
) *model.Event {
	t.Helper()

	eventID, err := types.EventIDFrom(eventIDValue)
	if err != nil {
		t.Fatalf("EventIDFrom(%q) error = %v", eventIDValue, err)
	}
	agent, err := types.AgentFrom(agentValue)
	if err != nil {
		t.Fatalf("AgentFrom(%q) error = %v", agentValue, err)
	}
	sessionID, err := types.SessionIDFrom(sessionIDValue)
	if err != nil {
		t.Fatalf("SessionIDFrom(%q) error = %v", sessionIDValue, err)
	}

	if sourceHook != "" {
		return model.EventOfWithSourceHook(
			eventID,
			kind,
			types.Client(client),
			agent,
			sessionID,
			types.Workspace(workspace),
			body,
			createdAt,
			sourceHook,
		)
	}

	return model.EventOf(
		eventID,
		kind,
		types.Client(client),
		agent,
		sessionID,
		types.Workspace(workspace),
		body,
		createdAt,
	)
}

func TestMemoryFamily_JSON_Goldens(t *testing.T) {
	accepted := mustMemoryDetails(t, "memory-golden-accepted", "Accepted golden memory", types.MemoryStatusAccepted)
	candidate := mustMemoryDetails(t, "memory-golden-candidate", "Candidate golden memory", types.MemoryStatusCandidate)
	rejected := mustMemoryDetails(t, "memory-golden-rejected", "Rejected golden memory", types.MemoryStatusRejected)
	superseded := mustMemoryDetails(t, "memory-golden-superseded", "Superseded golden memory", types.MemoryStatusSuperseded)
	expired := mustMemoryDetails(t, "memory-golden-expired", "Expired golden memory", types.MemoryStatusExpired)

	memoryStub := &memoryUsecaseStub{
		listResult:         []apptypes.MemorySummary{accepted.Summary()},
		searchResult:       []apptypes.MemorySummary{accepted.Summary()},
		showDetails:        accepted,
		rememberDetails:    accepted,
		proposeDetails:     candidate,
		acceptDetails:      accepted,
		rejectDetails:      rejected,
		supersedeDetails:   superseded,
		expireDetails:      expired,
		setValidityDetails: accepted,
	}
	importResult := apptypes.MemoryImportResult{
		Imported:              []apptypes.MemoryDetails{candidate},
		SkippedDuplicateCount: 2,
		SkippedRejectedCount:  1,
		Warnings:              []string{"ignored blank bullet"},
	}
	exportResult := apptypes.MemoryExportResult{
		Target:        apptypes.MemoryBridgeTargetCodex,
		Markdown:      "- Accepted golden memory\n",
		ExportedCount: 1,
	}
	hygieneSuggestion := apptypes.MemoryHygieneSuggestion{
		MemoryID:            accepted.Summary().MemoryID(),
		Kind:                apptypes.MemoryHygieneSuggestionSupersedeCandidate,
		Reason:              "newer overlapping decision",
		Fact:                "Accepted golden memory",
		ReplacementMemoryID: candidate.Summary().MemoryID(),
		ReplacementFact:     "Candidate golden memory",
		Similarity:          0.75,
		Scope:               accepted.Summary().Scope(),
		UpdatedAt:           time.Date(2026, 4, 14, 15, 0, 0, 0, time.UTC),
	}
	hygieneResult := apptypes.MemoryHygieneScanResult{
		Suggestions:             []apptypes.MemoryHygieneSuggestion{hygieneSuggestion},
		SupersedeCandidateCount: 1,
	}
	hygieneApplyResult := apptypes.MemoryHygieneApplyResult{
		Applied: []apptypes.MemoryHygieneApplied{{
			MemoryID:   accepted.Summary().MemoryID().String(),
			Kind:       apptypes.MemoryHygieneSuggestionSupersedeCandidate,
			Transition: "superseded",
			Details:    superseded,
		}},
		Failures: []apptypes.MemoryHygieneApplyFailure{{MemoryID: "memory-missing", Error: "no current hygiene suggestion"}},
	}
	memoryStub.extractDetails = []apptypes.MemoryDetails{candidate}
	memoryStub.importResult = importResult
	memoryStub.bridgeImportResult = apptypes.MemoryBridgeImportResult(importResult)
	memoryStub.exportResult = exportResult
	memoryStub.scanResult = hygieneResult
	memoryStub.applyResult = hygieneApplyResult
	edge := mustMemoryEdgeForGolden(t, "edge-golden-1")

	cases := []struct {
		name    string
		args    []string
		fixture string
	}{
		{"list", []string{"memory", "list", "--db-path", "/tmp/test-traceary.db", "--workspace", "github.com/duck8823/traceary", "--json"}, "list.golden.json"},
		{"search", []string{"memory", "search", "--db-path", "/tmp/test-traceary.db", "--workspace", "github.com/duck8823/traceary", "--json", "golden"}, "search.golden.json"},
		{"show", []string{"memory", "show", "--db-path", "/tmp/test-traceary.db", "--json", "memory-golden-accepted"}, "show.golden.json"},
		{"remember", []string{"memory", "remember", "--db-path", "/tmp/test-traceary.db", "--workspace", "github.com/duck8823/traceary", "--type", "decision", "--fact", "Accepted golden memory", "--json"}, "remember.golden.json"},
		{"propose", []string{"memory", "propose", "--db-path", "/tmp/test-traceary.db", "--workspace", "github.com/duck8823/traceary", "--type", "decision", "--fact", "Candidate golden memory", "--json"}, "propose.golden.json"},
		{"accept", []string{"memory", "accept", "--db-path", "/tmp/test-traceary.db", "--confidence", "verified", "--json", "memory-golden-candidate"}, "accept.golden.json"},
		{"reject", []string{"memory", "reject", "--db-path", "/tmp/test-traceary.db", "--json", "memory-golden-candidate"}, "reject.golden.json"},
		{"supersede", []string{"memory", "supersede", "--db-path", "/tmp/test-traceary.db", "--workspace", "github.com/duck8823/traceary", "--type", "decision", "--fact", "Superseded golden memory", "--json", "memory-golden-accepted"}, "supersede.golden.json"},
		{"expire", []string{"memory", "expire", "--db-path", "/tmp/test-traceary.db", "--at", "2026-04-15T00:00:00Z", "--json", "memory-golden-accepted"}, "expire.golden.json"},
		{"set-validity", []string{"memory", "set-validity", "--db-path", "/tmp/test-traceary.db", "--from", "2026-04-13T09:00:00Z", "--to", "2026-05-13T09:00:00Z", "--json", "memory-golden-accepted"}, "set-validity.golden.json"},
		{"extract", []string{"memory", "extract", "--db-path", "/tmp/test-traceary.db", "--session-id", "session-golden", "--json"}, "extract.golden.json"},
		{"import-codex", []string{"memory", "import", "codex", "--db-path", "/tmp/test-traceary.db", "--root", "/tmp/codex-memories", "--workspace", "github.com/duck8823/traceary", "--json"}, "import-codex.golden.json"},
		{"import-instructions", []string{"memory", "import", "instructions", "--db-path", "/tmp/test-traceary.db", "--source", "codex", "--in", "/tmp/AGENTS.md", "--workspace", "github.com/duck8823/traceary", "--json"}, "import-instructions.golden.json"},
		{"export", []string{"memory", "export", "--db-path", "/tmp/test-traceary.db", "--target", "codex", "--workspace", "github.com/duck8823/traceary", "--out", filepath.Join(t.TempDir(), "AGENTS.md"), "--json"}, "export.golden.json"},
		{"inbox-list", []string{"memory", "inbox", "list", "--db-path", "/tmp/test-traceary.db", "--workspace", "github.com/duck8823/traceary", "--json"}, "inbox-list.golden.json"},
		{"inbox-accept", []string{"memory", "inbox", "accept", "--db-path", "/tmp/test-traceary.db", "--ids", "memory-golden-candidate", "--confidence", "verified", "--json"}, "inbox-accept.golden.json"},
		{"inbox-reject", []string{"memory", "inbox", "reject", "--db-path", "/tmp/test-traceary.db", "--ids", "memory-golden-candidate", "--json"}, "inbox-reject.golden.json"},
		{"hygiene-scan", []string{"memory", "hygiene", "scan", "--db-path", "/tmp/test-traceary.db", "--workspace", "github.com/duck8823/traceary", "--json"}, "hygiene-scan.golden.json"},
		{"hygiene-apply", []string{"memory", "hygiene", "apply", "--db-path", "/tmp/test-traceary.db", "--ids", "memory-golden-accepted,memory-missing", "--json"}, "hygiene-apply.golden.json"},
		{"graph-add", []string{"memory", "graph", "add", "memory-golden-accepted", "--db-path", "/tmp/test-traceary.db", "--to", "memory-golden-candidate", "--relation", "supports", "--from", "2026-04-14T15:00:00Z", "--json"}, "graph-add.golden.json"},
		{"graph-list", []string{"memory", "graph", "list", "--db-path", "/tmp/test-traceary.db", "--memory-id", "memory-golden-accepted", "--json"}, "graph-list.golden.json"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			rootCmd := newTestRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithMemory(memoryStub),
				cli.WithMemoryEdge(&memoryEdgeUsecaseStub{addEdge: edge, listEdges: []*model.MemoryEdge{edge}}),
			).Command()
			rootCmd.SetOut(stdout)
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs(tc.args)

			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			assertJSONGolden(t, stdout.Bytes(), filepath.Join("testdata", "memory", tc.fixture))
		})
	}
}

func TestDoctor_JSON_Golden(t *testing.T) {
	homeDir := "/tmp/traceary-json-golden-home"
	projectDir := "/tmp/traceary-json-golden-project"
	t.Cleanup(func() {
		_ = os.RemoveAll(homeDir)
		_ = os.RemoveAll(projectDir)
	})
	_ = os.RemoveAll(homeDir)
	_ = os.RemoveAll(projectDir)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	t.Setenv("HOME", homeDir)
	setTracearyPathToCurrentExecutableAt(t, "/tmp/traceary-json-golden-bin")
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--db-path", "/tmp/test-traceary.db", "--client", "codex", "--project-dir", projectDir, "--json"})

	executeDoctorAllowWarnings(t, rootCmd)

	assertJSONGolden(t, stdout.Bytes(), filepath.Join("testdata", "doctor", "default.golden.json"))
}

func setTracearyPathToCurrentExecutableAt(t *testing.T, dir string) {
	t.Helper()
	current, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	link := filepath.Join(dir, "traceary")
	if err := os.Symlink(current, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	t.Setenv("PATH", dir)
}

func TestBundleImport_JSON_Golden(t *testing.T) {
	t.Setenv("TRACEARY_BUNDLE_PASSPHRASE", "golden-passphrase")
	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithBundle(&bundleUsecaseStub{importResult: usecase.BundleImportResult{EventsImported: 3, EventsSkipped: 1, BundleSchemaVersion: 12}}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"bundle", "import", "--db-path", "/tmp/test-traceary.db", "--in", "/tmp/traceary.golden.bundle", "--json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	assertJSONGolden(t, stdout.Bytes(), filepath.Join("testdata", "bundle", "import.golden.json"))
}

func TestTimeline_JSON_Golden(t *testing.T) {
	breakdown := []apptypes.TimelineWorkspaceBreakdown{
		apptypes.TimelineWorkspaceBreakdownOf(
			"github.com/duck8823/traceary",
			4,
			[]string{"command_executed", "command_executed", "note", "prompt"},
			[]string{"codex", "claude"},
			"Recorded memory and doctor JSON contracts",
			apptypes.TimelineSummarySourcePrompt,
		),
	}
	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(&eventUsecaseStub{timelineBlocks: []apptypes.TimelineBlock{
			apptypes.TimelineBlockOf(
				time.Date(2026, 4, 14, 9, 0, 0, 0, time.UTC),
				time.Date(2026, 4, 14, 10, 30, 30, 0, time.UTC),
				4,
				[]string{"codex", "claude"},
				breakdown,
			),
		}}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"timeline", "--db-path", "/tmp/test-traceary.db", "--json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	assertJSONGolden(t, stdout.Bytes(), filepath.Join("testdata", "timeline", "default.golden.json"))
}

func mustMemoryEdgeForGolden(t *testing.T, id string) *model.MemoryEdge {
	t.Helper()
	edgeID, err := types.MemoryEdgeIDFrom(id)
	if err != nil {
		t.Fatalf("MemoryEdgeIDFrom() error = %v", err)
	}
	edge, err := model.NewMemoryEdge(
		edgeID,
		mustMemoryIDForCLI(t, "memory-golden-accepted"),
		mustMemoryIDForCLI(t, "memory-golden-candidate"),
		types.MemoryEdgeRelationSupports,
		time.Date(2026, 4, 14, 15, 0, 0, 0, time.UTC),
		types.Some(time.Date(2026, 5, 14, 15, 0, 0, 0, time.UTC)),
		time.Date(2026, 4, 14, 15, 1, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewMemoryEdge() error = %v", err)
	}
	return edge
}
