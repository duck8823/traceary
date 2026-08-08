package cli_test

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation"
	cli "github.com/duck8823/traceary/presentation/cli"
)

func TestHookSessionStart_WakeInjection(t *testing.T) {
	// Not parallel: mutates HOME / hook state env for config and markers.
	const workspace = "github.com/duck8823/traceary"
	const wakingSession = "sess-waking"

	tests := []struct {
		name              string
		seed              func(ctx context.Context, t *testing.T, fx *wakeInjectionFixture)
		budgetJSON        string // empty = default 8192; "0" disables
		consolidationJSON string // optional independent threshold
		configBody        string // full config body when set (overrides budget/consolidation helpers)
		unusableConfig    bool
		secondStart       bool
		wantSubstrings    []string
		wantAbsent        []string
		wantEmpty         bool
		wantLenAtMost     int64
		wantExactSecond   bool // second start must be empty
	}{
		{
			name: "three eligible summaries appear newest first without event bodies",
			seed: func(ctx context.Context, t *testing.T, fx *wakeInjectionFixture) {
				fx.seedSummary(ctx, t, "sess-a", workspace, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), "alpha summary", false, "")
				fx.seedSummary(ctx, t, "sess-b", workspace, time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC), "bravo summary", false, "")
				fx.seedSummary(ctx, t, "sess-c", workspace, time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC), "charlie summary", false, "")
			},
			wantSubstrings: []string{
				"Previous session summaries",
				"charlie summary",
				"bravo summary",
				"alpha summary",
			},
			wantAbsent: []string{"event body must never appear"},
		},
		{
			name:      "zero eligible summaries yield empty stdout",
			seed:      func(context.Context, *testing.T, *wakeInjectionFixture) {},
			wantEmpty: true,
		},
		{
			name: "only degraded rows yield empty stdout",
			seed: func(ctx context.Context, t *testing.T, fx *wakeInjectionFixture) {
				fx.seedSummary(ctx, t, "sess-deg", workspace, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), "degraded only", true, "")
			},
			wantEmpty: true,
		},
		{
			name: "other workspace subagent and waking session are excluded",
			seed: func(ctx context.Context, t *testing.T, fx *wakeInjectionFixture) {
				fx.seedSummary(ctx, t, "sess-keep", workspace, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), "kept summary", false, "")
				fx.seedSummary(ctx, t, "sess-other", "github.com/other/repo", time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC), "other workspace", false, "")
				fx.seedSummary(ctx, t, "sess-parent", workspace, time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC), "parent summary", false, "")
				fx.seedSummary(ctx, t, "sess-child", workspace, time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC), "child summary", false, "sess-parent")
				// Waking session refinement must never inject into itself.
				fx.seedSummary(ctx, t, wakingSession, workspace, time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC), "waking self", false, "")
			},
			wantSubstrings: []string{"kept summary", "parent summary"},
			wantAbsent:     []string{"other workspace", "child summary", "waking self"},
		},
		{
			name: "newest summary alone exceeds budget injects nothing",
			seed: func(ctx context.Context, t *testing.T, fx *wakeInjectionFixture) {
				fx.seedSummary(ctx, t, "sess-big", workspace, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), strings.Repeat("X", 9000), false, "")
			},
			budgetJSON: "8192",
			wantEmpty:  true,
		},
		{
			name: "two fit but three do not yields exactly two within budget",
			seed: func(ctx context.Context, t *testing.T, fx *wakeInjectionFixture) {
				// ~100 byte summaries; budget sized for header + two only.
				fx.seedSummary(ctx, t, "sess-1", workspace, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), strings.Repeat("1", 80), false, "")
				fx.seedSummary(ctx, t, "sess-2", workspace, time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC), strings.Repeat("2", 80), false, "")
				fx.seedSummary(ctx, t, "sess-3", workspace, time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC), strings.Repeat("3", 80), false, "")
			},
			// header(~70) + 3*(80+1) + 2 separators ≈ 320; two summaries ≈ 233
			budgetJSON:     "250",
			wantSubstrings: []string{strings.Repeat("3", 80), strings.Repeat("2", 80)},
			wantAbsent:     []string{strings.Repeat("1", 80)},
			wantLenAtMost:  250,
		},
		{
			name: "second SessionStart for same session injects nothing",
			seed: func(ctx context.Context, t *testing.T, fx *wakeInjectionFixture) {
				fx.seedSummary(ctx, t, "sess-once", workspace, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), "once only", false, "")
			},
			secondStart:     true,
			wantSubstrings:  []string{"once only"},
			wantExactSecond: true,
		},
		{
			name: "independence from consolidation threshold",
			seed: func(ctx context.Context, t *testing.T, fx *wakeInjectionFixture) {
				fx.seedSummary(ctx, t, "sess-ind", workspace, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), "independent summary", false, "")
			},
			// filled in by dual-run subtest below when consolidationJSON set
			budgetJSON:        "8192",
			consolidationJSON: "65536",
			wantSubstrings:    []string{"independent summary"},
		},
		{
			name: "unusable config disables injection",
			seed: func(ctx context.Context, t *testing.T, fx *wakeInjectionFixture) {
				fx.seedSummary(ctx, t, "sess-cfg", workspace, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), "should not inject", false, "")
			},
			configBody:     `{invalid`,
			unusableConfig: true,
			wantEmpty:      true,
		},
		{
			name: "explicit budget zero disables injection",
			seed: func(ctx context.Context, t *testing.T, fx *wakeInjectionFixture) {
				fx.seedSummary(ctx, t, "sess-off", workspace, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), "disabled", false, "")
			},
			budgetJSON: "0",
			wantEmpty:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
			t.Setenv("TRACEARY_WORKSPACE", workspace)

			if tt.configBody != "" {
				writeWakeConfigFile(t, home, tt.configBody)
			} else if tt.budgetJSON != "" || tt.consolidationJSON != "" {
				writeWakeAndConsolidationConfig(t, home, tt.budgetJSON, tt.consolidationJSON)
			}

			fx := newWakeInjectionFixture(t)
			tt.seed(context.Background(), t, fx)

			stdout, err := runHookSessionStart(t, fx, wakingSession, workspace)
			if err != nil {
				t.Fatalf("first SessionStart error = %v", err)
			}
			assertWakeStdout(t, stdout, tt.wantSubstrings, tt.wantAbsent, tt.wantEmpty, tt.wantLenAtMost)

			if tt.secondStart {
				stdout2, err := runHookSessionStart(t, fx, wakingSession, workspace)
				if err != nil {
					t.Fatalf("second SessionStart error = %v", err)
				}
				if len(stdout2) != 0 {
					t.Fatalf("second SessionStart stdout = %q, want empty", stdout2)
				}
			}
		})
	}

	// Independence: same store/budget, different consolidation thresholds → identical bytes.
	t.Run("consolidation threshold does not change injection output", func(t *testing.T) {
		var outs [2]string
		for i, threshold := range []string{"65536", "1048576"} {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
			t.Setenv("TRACEARY_WORKSPACE", workspace)
			writeWakeAndConsolidationConfig(t, home, "8192", threshold)

			fx := newWakeInjectionFixture(t)
			fx.seedSummary(context.Background(), t, "sess-ind", workspace, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), "independent summary", false, "")
			out, err := runHookSessionStart(t, fx, wakingSession, workspace)
			if err != nil {
				t.Fatalf("SessionStart(threshold=%s) error = %v", threshold, err)
			}
			outs[i] = out
		}
		if diff := cmp.Diff(outs[0], outs[1]); diff != "" {
			t.Fatalf("stdout mismatch across consolidation thresholds (-64KiB +1MiB):\n%s", diff)
		}
		if !strings.Contains(outs[0], "independent summary") {
			t.Fatalf("expected injection content, got %q", outs[0])
		}
	})
}

func TestHookSessionStart_MissingRefinementsTableInjectsNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "ws")

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	// Stop before migration 46 (session_refinements).
	database := sqliteinfra.NewDatabase(dbPath, migrationsBeforeCLI(t, 46))
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(database))
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	eventDS := sqliteinfra.NewEventDatasource(database)
	sessionDS := sqliteinfra.NewSessionDatasource(database)
	sessionUC := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS)
	wakeDS := sqliteinfra.NewSessionWakeSummaryDatasource(database)

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithSession(sessionUC),
		cli.WithSessionWakeSummary(wakeDS),
		cli.WithDatabasePathSetter(database.SetPath),
	).Command()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(`{"session_id":"sess-legacy","cwd":"/tmp","hook_event_name":"SessionStart"}`))
	rootCmd.SetArgs([]string{"hook", "session", "codex", "start", "--db-path", dbPath})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\nstderr=%s", err, stderr.String())
	}
	if len(stdout.Bytes()) != 0 {
		t.Fatalf("stdout = %q, want empty on pre-refinements store", stdout.String())
	}
}

func TestHookKimi_WakeInjectionOnFirstUserPromptSubmit(t *testing.T) {
	const workspace = "github.com/duck8823/traceary"
	const sessionID = "kimi-sess-1"

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", workspace)

	fx := newWakeInjectionFixture(t)
	fx.seedSummary(context.Background(), t, "prior", workspace, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), "kimi prior summary", false, "")

	// SessionStart must write nothing (fire-and-forget path keeps nil output).
	startOut, err := runHookKimi(t, fx, "session-start", sessionID, workspace, "")
	if err != nil {
		t.Fatalf("kimi session-start error = %v", err)
	}
	if len(startOut) != 0 {
		t.Fatalf("kimi SessionStart stdout = %q, want empty", startOut)
	}

	// First UserPromptSubmit injects.
	prompt1, err := runHookKimi(t, fx, "user-prompt-submit", sessionID, workspace, "hello")
	if err != nil {
		t.Fatalf("kimi user-prompt-submit #1 error = %v", err)
	}
	if !strings.Contains(prompt1, "kimi prior summary") {
		t.Fatalf("first UserPromptSubmit stdout = %q, want prior summary", prompt1)
	}

	// Second injects nothing.
	prompt2, err := runHookKimi(t, fx, "user-prompt-submit", sessionID, workspace, "again")
	if err != nil {
		t.Fatalf("kimi user-prompt-submit #2 error = %v", err)
	}
	if len(prompt2) != 0 {
		t.Fatalf("second UserPromptSubmit stdout = %q, want empty", prompt2)
	}
}

func TestLoadConfig_WakeInjectionBudget(t *testing.T) {
	tests := []struct {
		name            string
		json            string
		unreadable      bool
		danglingSymlink bool
		want            int64
	}{
		{
			name: "absent config file resolves the 8 KiB default",
			want: presentation.DefaultWakeInjectionBudgetBytes,
		},
		{
			name: "absent key defaults to 8 KiB",
			json: `{}`,
			want: presentation.DefaultWakeInjectionBudgetBytes,
		},
		{
			name: "explicit zero disables",
			json: `{"wake_injection":{"budget_bytes":0}}`,
			want: 0,
		},
		{
			name: "custom budget is honoured",
			json: `{"wake_injection":{"budget_bytes":4096}}`,
			want: 4096,
		},
		{
			name: "malformed config file resolves budget 0",
			json: `{invalid`,
			want: 0,
		},
		{
			name:       "unreadable config file resolves budget 0",
			json:       `{"wake_injection":{"budget_bytes":4096}}`,
			unreadable: true,
			want:       0,
		},
		{
			name:            "dangling config symlink resolves budget 0",
			danglingSymlink: true,
			want:            0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			configDir := filepath.Join(home, ".config", "traceary")
			if tt.json != "" || tt.danglingSymlink {
				if err := os.MkdirAll(configDir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(configDir, "config.json")
			if tt.danglingSymlink {
				if err := os.Symlink(filepath.Join(configDir, "missing-target.json"), path); err != nil {
					t.Fatal(err)
				}
			} else if tt.json != "" {
				if err := os.WriteFile(path, []byte(tt.json), 0o644); err != nil {
					t.Fatal(err)
				}
				if tt.unreadable {
					if err := os.Chmod(path, 0o000); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
				}
			}
			cfg := presentation.LoadConfig()
			if cfg.WakeInjection.BudgetBytes != tt.want {
				t.Fatalf("BudgetBytes = %d, want %d", cfg.WakeInjection.BudgetBytes, tt.want)
			}
		})
	}
}

type wakeInjectionFixture struct {
	dbPath      string
	database    *sqliteinfra.Database
	sessions    *sqliteinfra.SessionDatasource
	events      *sqliteinfra.EventDatasource
	refinements *sqliteinfra.SessionRefinementDatasource
	wake        *sqliteinfra.SessionWakeSummaryDatasource
	storeUC     usecase.StoreManagementUsecase
	sessionUC   usecase.SessionUsecase
	eventUC     usecase.EventUsecase
}

func newWakeInjectionFixture(t *testing.T) *wakeInjectionFixture {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(database))
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	eventDS := sqliteinfra.NewEventDatasource(database)
	sessionDS := sqliteinfra.NewSessionDatasource(database)
	return &wakeInjectionFixture{
		dbPath:      dbPath,
		database:    database,
		sessions:    sessionDS,
		events:      eventDS,
		refinements: sqliteinfra.NewSessionRefinementDatasource(database),
		wake:        sqliteinfra.NewSessionWakeSummaryDatasource(database),
		storeUC:     storeUC,
		sessionUC:   usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS),
		eventUC:     usecase.NewEventUsecase(eventDS, eventDS),
	}
}

func (fx *wakeInjectionFixture) seedSummary(
	ctx context.Context,
	t *testing.T,
	sessionID string,
	workspace string,
	startedAt time.Time,
	summary string,
	degraded bool,
	parentSessionID string,
) {
	t.Helper()
	ws := types.Workspace(workspace)
	var session *model.Session
	if parentSessionID != "" {
		parent, err := fx.sessions.FindByID(ctx, types.SessionID(parentSessionID))
		if err != nil {
			t.Fatalf("FindByID(%s) error = %v", parentSessionID, err)
		}
		parentVal, ok := parent.Value()
		if !ok || parentVal == nil {
			t.Fatalf("parent %s not found", parentSessionID)
		}
		session = model.NewChildSession(
			parentVal,
			types.SessionID(sessionID),
			startedAt,
			"codex",
			ws,
			types.EventID(sessionID+"-spawn"),
			"explore",
			1,
		)
	} else {
		session = model.NewSession(types.SessionID(sessionID), startedAt, "cli", "codex", ws)
	}
	boundary, err := model.NewEventWithClock(
		types.EventID(sessionID+"-start"),
		types.EventKindSessionStarted,
		"cli",
		"codex",
		types.SessionID(sessionID),
		ws,
		"session started",
		fixedClock{at: startedAt},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.sessions.SaveBoundary(ctx, session, boundary); err != nil {
		t.Fatalf("SaveBoundary(%s) error = %v", sessionID, err)
	}
	note, err := model.NewEventWithClock(
		types.EventID(sessionID+"-note"),
		types.EventKindNote,
		"cli",
		"codex",
		types.SessionID(sessionID),
		ws,
		"event body must never appear in wake injection for "+sessionID,
		fixedClock{at: startedAt.Add(time.Second)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.events.Save(ctx, note); err != nil {
		t.Fatalf("Save(note %s) error = %v", sessionID, err)
	}
	refinement, err := model.NewSessionRefinement(
		types.SessionID(sessionID),
		1,
		types.EventID(sessionID+"-start"),
		types.EventID(sessionID+"-note"),
		summary,
		"",
		"agent",
		startedAt.Add(2*time.Second),
		degraded,
	)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := fx.refinements.SaveIfAdvances(ctx, refinement, 0)
	if err != nil || !ok {
		t.Fatalf("SaveIfAdvances(%s) ok=%v err=%v", sessionID, ok, err)
	}
}

func runHookSessionStart(t *testing.T, fx *wakeInjectionFixture, sessionID, workspace string) (string, error) {
	t.Helper()
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(fx.storeUC),
		cli.WithSession(fx.sessionUC),
		cli.WithSessionWakeSummary(fx.wake),
		cli.WithDatabasePathSetter(fx.database.SetPath),
	).Command()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	// TRACEARY_WORKSPACE is set by the caller for a stable workspace key.
	_ = workspace
	payload := `{"session_id":"` + sessionID + `","cwd":"/tmp","hook_event_name":"SessionStart"}`
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetArgs([]string{"hook", "session", "codex", "start", "--db-path", fx.dbPath})
	err := rootCmd.Execute()
	if err != nil {
		return stdout.String(), err
	}
	return stdout.String(), nil
}

func runHookKimi(t *testing.T, fx *wakeInjectionFixture, action, sessionID, workspace, prompt string) (string, error) {
	t.Helper()
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(fx.storeUC),
		cli.WithSession(fx.sessionUC),
		cli.WithEvent(fx.eventUC),
		cli.WithSessionWakeSummary(fx.wake),
		cli.WithDatabasePathSetter(fx.database.SetPath),
	).Command()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	_ = workspace
	payload := `{"session_id":"` + sessionID + `","cwd":"/tmp"`
	if prompt != "" {
		payload += `,"prompt":"` + prompt + `"`
	}
	payload += `}`
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetArgs([]string{"hook", "kimi", action, "--db-path", fx.dbPath})
	err := rootCmd.Execute()
	return stdout.String(), err
}

func assertWakeStdout(t *testing.T, stdout string, wantSub, wantAbsent []string, wantEmpty bool, maxLen int64) {
	t.Helper()
	if wantEmpty {
		if len(stdout) != 0 {
			t.Fatalf("stdout length = %d (%q), want 0", len(stdout), stdout)
		}
		return
	}
	for _, sub := range wantSub {
		if !strings.Contains(stdout, sub) {
			t.Fatalf("stdout missing %q:\n%s", sub, stdout)
		}
	}
	for _, absent := range wantAbsent {
		if strings.Contains(stdout, absent) {
			t.Fatalf("stdout unexpectedly contains %q:\n%s", absent, stdout)
		}
	}
	if maxLen > 0 && int64(len(stdout)) > maxLen {
		t.Fatalf("stdout len %d exceeds budget %d", len(stdout), maxLen)
	}
}

func writeWakeConfigFile(t *testing.T, home, body string) {
	t.Helper()
	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeWakeAndConsolidationConfig(t *testing.T, home, budget, consolidation string) {
	t.Helper()
	parts := make([]string, 0, 2)
	if budget != "" {
		parts = append(parts, `"wake_injection":{"budget_bytes":`+budget+`}`)
	}
	if consolidation != "" {
		parts = append(parts, `"consolidation":{"threshold_bytes":`+consolidation+`}`)
	}
	body := `{` + strings.Join(parts, ",") + `}`
	writeWakeConfigFile(t, home, body)
}

func migrationsBeforeCLI(t *testing.T, beforeVersion int) fs.FS {
	t.Helper()
	root := filepath.Join("..", "..", "schema", "sqlite", "migrations")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	migrations := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		if len(entry.Name()) < 6 {
			continue
		}
		version, err := strconv.Atoi(entry.Name()[:6])
		if err != nil {
			continue
		}
		if version >= beforeVersion {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		migrations[entry.Name()] = &fstest.MapFile{Data: data}
	}
	return migrations
}
