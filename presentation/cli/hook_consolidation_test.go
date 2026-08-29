package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation"
	cli "github.com/duck8823/traceary/presentation/cli"
)

type consolidationPressureStub struct {
	result usecase.ConsolidationPressureResult
	err    error
	calls  int
}

func (s *consolidationPressureStub) Check(
	_ context.Context,
	_ types.SessionID,
	_ usecase.ConsolidationPolicy,
) (usecase.ConsolidationPressureResult, error) {
	s.calls++
	return s.result, s.err
}

func TestHookTranscript_ConsolidationPressure(t *testing.T) {
	// Not parallel: mutates HOME / hook state env for config and spool isolation.
	const sessionID = "sess-consol-e2e"

	tests := []struct {
		name              string
		client            string
		thresholdJSON     string // empty = omit key (default 64 KiB); "0" disables
		seedBytes         int    // note-body bytes (must not trigger)
		commandCount      int    // command_executed events since covers_to
		coveredBytes      int    // body on the covers_to boundary event (excluded from pressure)
		withRefinement    bool
		pressureErr       error // when set, injects a stub that errors instead of real store
		recordErr         error // when set, transcript Log fails; consolidation must not fire
		noExtractableText bool  // omit assistant text so extractor fail-softs (recorded=false)
		unresolvedSession bool  // force session resolve to empty (recorded=false, err=nil)
		stopHookActive    bool  // payload carries stop_hook_active:true (continuation a hook caused)
		wantExitCode      int
		wantStderrSub     []string
		wantNoFire        bool // pressure usecase must not be consulted (host gate / disabled / record fail / fail-soft skip)
	}{
		{
			name:         "63 KiB unrefined exits 0 with empty stderr reason",
			client:       "claude",
			seedBytes:    63 * 1024,
			wantExitCode: 0,
		},
		{
			name:          "a large body with no commands exits 0",
			client:        "claude",
			seedBytes:     65 * 1024,
			wantExitCode:  0,
			wantStderrSub: nil,
		},
		{
			name:         "work-based due exits 2 with signal work in the ledger",
			client:       "claude",
			commandCount: 20,
			wantExitCode: 2,
			wantStderrSub: []string{
				sessionID,
				"unrefined material",
				"why the work was undertaken",
				"what changed",
				"how it went",
				"traceary-session-refine",
				"--produced-by agent",
			},
		},
		{
			name:         "below min_commands exits 0",
			client:       "claude",
			commandCount: 19,
			wantExitCode: 0,
		},
		{
			name:         "codex work-based due exits 2",
			client:       "codex",
			commandCount: 20,
			wantExitCode: 2,
			wantStderrSub: []string{
				sessionID,
				"unrefined material",
				"traceary-session-refine",
			},
		},
		{
			name:         "codex below min_commands exits 0",
			client:       "codex",
			commandCount: 19,
			wantExitCode: 0,
		},
		{
			name:         "codex a large body with no commands exits 0",
			client:       "codex",
			seedBytes:    65 * 1024,
			wantExitCode: 0,
		},
		{
			name:           "stop_hook_active true suppresses the request even when over threshold",
			client:         "claude",
			seedBytes:      65 * 1024,
			stopHookActive: true,
			wantExitCode:   0,
			wantNoFire:     true,
		},
		{
			name:           "previous refinement summary and covers_to appear in reason",
			client:         "codex",
			commandCount:   20,
			withRefinement: true,
			wantExitCode:   2,
			wantStderrSub:  []string{sessionID, "previous fold summary", "covers_to=evt-cover", "why the work was undertaken", "what changed"},
		},
		{
			name:          "min_commands 0 never fires",
			client:        "claude",
			thresholdJSON: `{"min_commands":0}`,
			commandCount:  20,
			wantExitCode:  0,
			wantNoFire:    true,
		},
		{
			name:         "database error fails open with exit 0",
			client:       "claude",
			pressureErr:  errors.New("database is locked"),
			wantExitCode: 0,
		},
		{
			name:         "gemini never fires",
			client:       "gemini",
			commandCount: 20,
			wantExitCode: 0,
			wantNoFire:   true,
		},
		{
			name:         "antigravity never fires",
			client:       "antigravity",
			commandCount: 20,
			wantExitCode: 0,
			wantNoFire:   true,
		},
		{
			name:           "events at or before covers_to are excluded from pressure",
			client:         "kimi",
			coveredBytes:   65 * 1024,
			seedBytes:      0,
			withRefinement: true,
			wantExitCode:   0,
		},
		{
			name:         "recording failure does not request consolidation when pressure is over threshold",
			client:       "codex",
			commandCount: 20,
			recordErr:    errors.New("disk full"),
			wantExitCode: 0,
			wantNoFire:   true,
		},
		{
			name:              "no extractable content does not request consolidation when pressure is over threshold",
			client:            "codex",
			commandCount:      20,
			noExtractableText: true,
			wantExitCode:      0,
			wantNoFire:        true,
		},
		{
			name:              "unresolved session does not request consolidation when pressure is over threshold",
			client:            "codex",
			commandCount:      20,
			unresolvedSession: true,
			wantExitCode:      0,
			wantNoFire:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
			t.Setenv("TRACEARY_WORKSPACE", "github.com/dogfood/test")

			if tt.thresholdJSON != "" {
				writeConsolidationConfig(t, home, tt.thresholdJSON)
			}

			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "traceary.db")
			db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
			eventDS := sqliteinfra.NewEventDatasource(db)
			sessionDS := sqliteinfra.NewSessionDatasource(db)
			refinementDS := sqliteinfra.NewSessionRefinementDatasource(db)
			storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
			eventUC := usecase.NewEventUsecase(eventDS, eventDS)
			if err := storeUC.Initialize(ctx); err != nil {
				t.Fatalf("Initialize() error = %v", err)
			}

			base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
			session := model.NewSession(types.SessionID(sessionID), base, "cli", "codex", "ws")
			start, err := model.NewEventWithClock(
				"evt-start", types.EventKindSessionStarted, "cli", "codex",
				types.SessionID(sessionID), "ws", "start",
				fixedClock{at: base},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := sessionDS.SaveBoundary(ctx, session, start); err != nil {
				t.Fatal(err)
			}

			if tt.withRefinement {
				// Boundary event covered by the refinement. Body is excluded
				// from pressure (strictly-after covers_to).
				coverBody := "covered"
				if tt.coveredBytes > 0 {
					coverBody = strings.Repeat("c", tt.coveredBytes)
				}
				covered, err := model.NewEventWithClock(
					"evt-cover", types.EventKindNote, "cli", "codex",
					types.SessionID(sessionID), "ws", coverBody,
					fixedClock{at: base.Add(time.Second)},
				)
				if err != nil {
					t.Fatal(err)
				}
				if err := eventDS.Save(ctx, covered); err != nil {
					t.Fatal(err)
				}
				row, err := model.NewSessionRefinement(
					types.SessionID(sessionID), 1, "evt-start", "evt-cover",
					"previous fold summary", "", "agent", base.Add(2*time.Second), false,
				)
				if err != nil {
					t.Fatal(err)
				}
				if ok, err := refinementDS.SaveIfAdvances(ctx, row, 0); err != nil || !ok {
					t.Fatalf("SaveIfAdvances() ok=%v err=%v", ok, err)
				}
			}

			if tt.commandCount > 0 {
				for i := 0; i < tt.commandCount; i++ {
					cmd, err := model.NewEventWithClock(
						types.EventID("evt-cmd-"+strconv.Itoa(i)), types.EventKindCommandExecuted, "cli", "codex",
						types.SessionID(sessionID), "ws", "cmd",
						fixedClock{at: base.Add(time.Duration(4+i) * time.Second)},
					)
					if err != nil {
						t.Fatal(err)
					}
					if err := eventDS.Save(ctx, cmd); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tt.seedBytes > 0 {
				// Place the large body after the refinement boundary (or at
				// t+1s when there is no refinement) so pressure after covers_to
				// sees it and the covered small event does not.
				at := base.Add(3 * time.Second)
				body := strings.Repeat("x", tt.seedBytes)
				event, err := model.NewEventWithClock(
					"evt-heavy", types.EventKindNote, "cli", "codex",
					types.SessionID(sessionID), "ws", body,
					fixedClock{at: at},
				)
				if err != nil {
					t.Fatal(err)
				}
				if err := eventDS.Save(ctx, event); err != nil {
					t.Fatal(err)
				}
			}

			var pressureUC usecase.ConsolidationPressureUsecase
			var stub *consolidationPressureStub
			if tt.pressureErr != nil {
				stub = &consolidationPressureStub{err: tt.pressureErr}
				pressureUC = stub
			} else if tt.wantNoFire {
				// Stub so we can assert the host/threshold gate short-circuits
				// before any pressure measurement (or that recording failure
				// never reaches the pressure check).
				stub = &consolidationPressureStub{
					result: usecase.ConsolidationPressureResult{Commands: int64(tt.commandCount), Due: true},
				}
				pressureUC = stub
			} else {
				ledger := sqliteinfra.NewConsolidationRequestDatasource(db)
				pressureUC = usecase.NewConsolidationPressureUsecase(eventDS, refinementDS, sessionDS, ledger)
			}

			// Transcript capture needs a payload the client extractor accepts.
			// Codex last_assistant_message is the simplest cross-client path
			// that still resolves session_id from the JSON body.
			// Claude stop payload uses transcript_path; for clients without a
			// message field the durable path fail-softs. With a successful
			// record path, consolidation still runs on the session_id.
			payload := `{"session_id":"` + sessionID + `","cwd":"/tmp","last_assistant_message":"ok","prompt_response":"ok"}`
			if tt.stopHookActive {
				payload = `{"session_id":"` + sessionID + `","cwd":"/tmp","last_assistant_message":"ok","prompt_response":"ok","stop_hook_active":true}`
			}
			if tt.noExtractableText {
				// Keep session_id so resolution would succeed; omit assistant
				// text so the extractor returns no blocks (recorded=false).
				payload = `{"session_id":"` + sessionID + `","cwd":"/tmp"}`
			}
			if tt.unresolvedSession {
				cli.SetResolveHookTranscriptSessionIDFunc(func(_ []byte, _ string) (types.SessionID, error) {
					return "", nil
				})
				t.Cleanup(cli.ResetResolveHookTranscriptSessionIDFunc)
			}

			var eventOpt cli.RootCLIOption
			if tt.recordErr != nil {
				eventOpt = cli.WithEvent(&eventUsecaseStub{logErr: tt.recordErr})
			} else {
				eventOpt = cli.WithEvent(eventUC)
			}

			stderr := &bytes.Buffer{}
			rootCmd := cli.NewRootCLI(
				cli.WithStoreManagement(storeUC),
				eventOpt,
				cli.WithConsolidationPressure(pressureUC),
				cli.WithDatabasePathSetter(db.SetPath),
			).Command()
			rootCmd.SetIn(strings.NewReader(payload))
			rootCmd.SetOut(&bytes.Buffer{})
			rootCmd.SetErr(stderr)
			rootCmd.SetArgs([]string{"hook", "transcript", tt.client, "--db-path", dbPath})

			err = rootCmd.Execute()
			gotCode := 0
			if err != nil {
				var coder interface{ ExitCode() int }
				if errors.As(err, &coder) {
					gotCode = coder.ExitCode()
				} else {
					t.Fatalf("Execute() error = %v (type %T), want ExitCode", err, err)
				}
			}
			if gotCode != tt.wantExitCode {
				t.Fatalf("exit code = %d, want %d; err=%v stderr=%q", gotCode, tt.wantExitCode, err, stderr.String())
			}

			// The error message itself is what main writes to stderr; cobra
			// may not copy it into SetErr. Assert on err.Error() when due.
			message := ""
			if err != nil {
				message = err.Error()
			}
			for _, sub := range tt.wantStderrSub {
				if !strings.Contains(message, sub) {
					t.Fatalf("reason %q does not contain %q", message, sub)
				}
			}
			if tt.wantExitCode == 0 && message != "" && tt.pressureErr == nil && !tt.wantNoFire {
				// Non-due paths should not surface a consolidation reason.
				if strings.Contains(message, "unrefined material") {
					t.Fatalf("unexpected consolidation reason on exit 0: %q", message)
				}
			}
			if tt.wantNoFire && stub != nil && stub.calls != 0 {
				t.Fatalf("pressure usecase called %d times, want 0 (gate should short-circuit)", stub.calls)
			}
			if tt.pressureErr != nil && stub != nil && stub.calls != 1 {
				t.Fatalf("pressure usecase calls = %d, want 1", stub.calls)
			}

			// Sanity: default threshold resolves when key is absent.
			if tt.thresholdJSON == "" {
				cfg := presentation.LoadConfig()
				if cfg.Consolidation.ThresholdBytes != presentation.DefaultConsolidationThresholdBytes {
					t.Fatalf("default ThresholdBytes = %d, want %d",
						cfg.Consolidation.ThresholdBytes, presentation.DefaultConsolidationThresholdBytes)
				}
			}
		})
	}
}

func TestHookTranscript_ThresholdBytesWarnDoesNotTrigger(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "github.com/dogfood/test")
	writeConsolidationConfig(t, home, `{"threshold_bytes":131072}`)

	warn := &bytes.Buffer{}
	cli.ResetConsolidationDeprecationWarnForTest()
	cli.SetConsolidationWarnWriterForTest(warn)
	t.Cleanup(func() {
		cli.SetConsolidationWarnWriterForTest(nil)
		cli.ResetConsolidationDeprecationWarnForTest()
	})

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	sessionDS := sqliteinfra.NewSessionDatasource(db)
	refinementDS := sqliteinfra.NewSessionRefinementDatasource(db)
	ledger := sqliteinfra.NewConsolidationRequestDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	session := model.NewSession("sess-warn", base, "cli", "claude", "ws")
	start, err := model.NewEventWithClock(
		"evt-start", types.EventKindSessionStarted, "cli", "claude",
		"sess-warn", "ws", "start",
		fixedClock{at: base},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessionDS.SaveBoundary(ctx, session, start); err != nil {
		t.Fatal(err)
	}

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithEvent(usecase.NewEventUsecase(eventDS, eventDS)),
		cli.WithConsolidationPressure(usecase.NewConsolidationPressureUsecase(eventDS, refinementDS, sessionDS, ledger)),
		cli.WithConsolidationRequest(usecase.NewConsolidationRequestUsecase(ledger, types.SystemClock{})),
		cli.WithSessionEventOrder(eventDS),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	payload := `{"session_id":"sess-warn","cwd":"/tmp","last_assistant_message":"ok","prompt_response":"ok"}`
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"hook", "transcript", "claude", "--db-path", dbPath})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	wantWarn := "[WARN] consolidation.threshold_bytes is deprecated and ignored: the stop-hook consolidation trigger is now work-based (consolidation.min_commands, consolidation.stop_cadence). The key is removed in the next minor; remove it from your config.json.\n"
	if warn.String() != wantWarn {
		t.Fatalf("warn = %q, want %q", warn.String(), wantWarn)
	}
}

// TestHookKimiStop_ConsolidationPressure pins the native Kimi Stop path
// (`traceary hook kimi stop`) to the same consolidation gate as
// `hook transcript`: exit 2 only when a turn was recorded on this firing
// and pressure is over threshold. Idempotent redeliveries that write nothing
// must stay exit 0 even when pressure remains due (#1674 / #1711).
func TestHookTranscript_WorkBasedCadenceAndSubagent(t *testing.T) {
	for _, client := range []string{"claude", "codex"} {
		t.Run(client+"/second stop inside the cadence window exits 0", func(t *testing.T) {
			fx := newConsolidationHookFixture(t, "sess-cadence-"+client)
			code, _ := fx.runTranscriptClient(t, client, "one")
			if code != 2 {
				t.Fatalf("first exit = %d, want 2", code)
			}
			code, message := fx.runTranscriptClient(t, client, "two")
			if code != 0 {
				t.Fatalf("second exit = %d, want 0; %s", code, message)
			}
			if strings.Contains(message, "unrefined material") {
				t.Fatalf("cadence window emitted a reason: %q", message)
			}
		})
		t.Run(client+"/stop after the cadence window exits 2 again", func(t *testing.T) {
			fx := newConsolidationHookFixture(t, "sess-after-"+client)
			if code, _ := fx.runTranscriptClient(t, client, "one"); code != 2 {
				t.Fatalf("first exit = %d, want 2", code)
			}
			seedTranscriptsAfterNow(t, fx.eventDS, fx.sessionID, "gap-"+client, 8)
			code, message := fx.runTranscriptClient(t, client, "after")
			if code != 2 {
				t.Fatalf("after cadence exit = %d, want 2; %s", code, message)
			}
		})
		t.Run(client+"/subagent session exits 0", func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
			t.Setenv("TRACEARY_WORKSPACE", "github.com/dogfood/test")
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "traceary.db")
			db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
			eventDS := sqliteinfra.NewEventDatasource(db)
			sessionDS := sqliteinfra.NewSessionDatasource(db)
			refinementDS := sqliteinfra.NewSessionRefinementDatasource(db)
			ledger := sqliteinfra.NewConsolidationRequestDatasource(db)
			storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
			if err := storeUC.Initialize(ctx); err != nil {
				t.Fatalf("Initialize() error = %v", err)
			}
			base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
			parent := model.NewSession(types.SessionID("sess-parent-"+client), base, "cli", types.Agent(client), "ws")
			parentStart, err := model.NewEventWithClock(
				"evt-parent", types.EventKindSessionStarted, "cli", types.Agent(client),
				parent.SessionID(), "ws", "start",
				fixedClock{at: base},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := sessionDS.SaveBoundary(ctx, parent, parentStart); err != nil {
				t.Fatal(err)
			}
			childID := types.SessionID("sess-child-" + client)
			child := model.NewChildSession(parent, childID, base, types.Agent(client), "ws", "evt-parent", "explore", 1)
			childStart, err := model.NewEventWithClock(
				"evt-child", types.EventKindSessionStarted, "cli", types.Agent(client),
				childID, "ws", "start",
				fixedClock{at: base.Add(time.Second)},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := sessionDS.SaveBoundary(ctx, child, childStart); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 20; i++ {
				cmd, err := model.NewEventWithClock(
					types.EventID("evt-child-cmd-"+strconv.Itoa(i)), types.EventKindCommandExecuted, "cli", types.Agent(client),
					childID, "ws", "cmd",
					fixedClock{at: base.Add(time.Duration(i+2) * time.Second)},
				)
				if err != nil {
					t.Fatal(err)
				}
				if err := eventDS.Save(ctx, cmd); err != nil {
					t.Fatal(err)
				}
			}
			rootCmd := cli.NewRootCLI(
				cli.WithStoreManagement(storeUC),
				cli.WithEvent(usecase.NewEventUsecase(eventDS, eventDS)),
				cli.WithConsolidationPressure(usecase.NewConsolidationPressureUsecase(eventDS, refinementDS, sessionDS, ledger)),
				cli.WithConsolidationRequest(usecase.NewConsolidationRequestUsecase(ledger, types.SystemClock{})),
				cli.WithSessionEventOrder(eventDS),
				cli.WithDatabasePathSetter(db.SetPath),
			).Command()
			payload := `{"session_id":"` + childID.String() + `","cwd":"/tmp","last_assistant_message":"child","prompt_response":"child"}`
			rootCmd.SetIn(strings.NewReader(payload))
			rootCmd.SetOut(&bytes.Buffer{})
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs([]string{"hook", "transcript", client, "--db-path", dbPath})
			err = rootCmd.Execute()
			if err != nil {
				t.Fatalf("subagent Execute() error = %v", err)
			}
		})
	}
}

func TestHookKimiStop_ConsolidationPressure(t *testing.T) {
	// Not parallel: mutates HOME / hook state env for Kimi wire log + spool.
	const sessionID = "session_00000000-0000-4000-8000-000000000001"

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "github.com/dogfood/test")
	cli.SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	// Wire log with a completed turn so the first Stop persists a transcript
	// row; the second Stop hits the per-(session, turn, fingerprint) marker
	// and writes nothing.
	seedKimiSession(t, home, sessionID, []string{
		`{"type":"metadata","protocol_version":"1.4","created_at":1784466738324}`,
		`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"text","text":"kimi stop consolidation probe"}}}`,
	})

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	sessionDS := sqliteinfra.NewSessionDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	eventUC := usecase.NewEventUsecase(eventDS, eventDS)
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	session := model.NewSession(types.SessionID(sessionID), base, "cli", "kimi", "ws")
	start, err := model.NewEventWithClock(
		"evt-start", types.EventKindSessionStarted, "cli", "kimi",
		types.SessionID(sessionID), "ws", "start",
		fixedClock{at: base},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessionDS.SaveBoundary(ctx, session, start); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		cmd, err := model.NewEventWithClock(
			types.EventID("evt-kimi-cmd-"+strconv.Itoa(i)), types.EventKindCommandExecuted, "cli", "kimi",
			types.SessionID(sessionID), "ws", "cmd",
			fixedClock{at: base.Add(time.Duration(i+2) * time.Second)},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := eventDS.Save(ctx, cmd); err != nil {
			t.Fatal(err)
		}
	}

	// Stub reports Due so the test does not depend on work arithmetic; the
	// gate under test is "was a row written on this firing?".
	stub := &consolidationPressureStub{
		result: usecase.ConsolidationPressureResult{
			Commands: 20,
			Due:      true,
		},
	}
	pressureUC := stub

	payload := readKimiFixture(t, "stop.json")

	runStop := func(t *testing.T) (exitCode int, message string) {
		t.Helper()
		stderr := &bytes.Buffer{}
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(storeUC),
			cli.WithEvent(eventUC),
			cli.WithConsolidationPressure(pressureUC),
			cli.WithDatabasePathSetter(db.SetPath),
		).Command()
		rootCmd.SetIn(strings.NewReader(payload))
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(stderr)
		rootCmd.SetArgs([]string{"hook", "kimi", "stop", "--db-path", dbPath})

		err := rootCmd.Execute()
		if err == nil {
			return 0, ""
		}
		var coder interface{ ExitCode() int }
		if errors.As(err, &coder) {
			return coder.ExitCode(), err.Error()
		}
		t.Fatalf("Execute() error = %v (type %T), want ExitCode or nil", err, err)
		return 0, ""
	}

	t.Run("recorded turn over threshold exits 2 with reason on stderr", func(t *testing.T) {
		gotCode, message := runStop(t)
		if diff := cmp.Diff(2, gotCode); diff != "" {
			t.Fatalf("exit code mismatch (-want +got):\n%s; message=%q", diff, message)
		}
		for _, sub := range []string{sessionID, "unrefined material", "traceary-session-refine"} {
			if !strings.Contains(message, sub) {
				t.Fatalf("reason %q does not contain %q", message, sub)
			}
		}
		if stub.calls != 1 {
			t.Fatalf("pressure usecase calls = %d, want 1 after the recording firing", stub.calls)
		}
	})

	t.Run("idempotent redelivery exits 0 even when pressure is still over threshold", func(t *testing.T) {
		// Same wire log + marker from the first subtest: nothing is recorded
		// on this firing, so consolidation must not run again.
		callsBefore := stub.calls
		gotCode, message := runStop(t)
		if diff := cmp.Diff(0, gotCode); diff != "" {
			t.Fatalf("exit code mismatch (-want +got):\n%s; message=%q", diff, message)
		}
		if strings.Contains(message, "unrefined material") {
			t.Fatalf("unexpected consolidation reason on idempotent skip: %q", message)
		}
		if stub.calls != callsBefore {
			t.Fatalf("pressure usecase calls = %d, want %d (idempotent skip must not re-check)", stub.calls, callsBefore)
		}
	})
}

func TestHookKimiStop_ConsolidationPressure_StopHookActiveSuppresses(t *testing.T) {
	// Separate fixture: Kimi's turn marker is per-store, so this must be the
	// first recording firing — not a second Stop on the sequential pair above.
	const sessionID = "session_00000000-0000-4000-8000-000000000001"

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "github.com/dogfood/test")
	cli.SetUserHomeDirFunc(func() (string, error) { return home, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	seedKimiSession(t, home, sessionID, []string{
		`{"type":"metadata","protocol_version":"1.4","created_at":1784466738324}`,
		`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"text","text":"kimi stop consolidation probe"}}}`,
	})

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	sessionDS := sqliteinfra.NewSessionDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	eventUC := usecase.NewEventUsecase(eventDS, eventDS)
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	session := model.NewSession(types.SessionID(sessionID), base, "cli", "kimi", "ws")
	start, err := model.NewEventWithClock(
		"evt-start", types.EventKindSessionStarted, "cli", "kimi",
		types.SessionID(sessionID), "ws", "start",
		fixedClock{at: base},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessionDS.SaveBoundary(ctx, session, start); err != nil {
		t.Fatal(err)
	}
	heavy, err := model.NewEventWithClock(
		"evt-heavy", types.EventKindNote, "cli", "kimi",
		types.SessionID(sessionID), "ws", strings.Repeat("x", 65*1024),
		fixedClock{at: base.Add(time.Second)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := eventDS.Save(ctx, heavy); err != nil {
		t.Fatal(err)
	}

	stub := &consolidationPressureStub{
		result: usecase.ConsolidationPressureResult{
			Commands: 20,
			Due:      true,
		},
	}

	var fixture map[string]any
	if err := json.Unmarshal([]byte(readKimiFixture(t, "stop.json")), &fixture); err != nil {
		t.Fatalf("unmarshal Kimi stop fixture: %v", err)
	}
	fixture["stop_hook_active"] = true
	payload, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal toggled Kimi stop fixture: %v", err)
	}

	stderr := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithEvent(eventUC),
		cli.WithConsolidationPressure(stub),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	rootCmd.SetIn(bytes.NewReader(payload))
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"hook", "kimi", "stop", "--db-path", dbPath})

	execErr := rootCmd.Execute()
	gotCode := 0
	message := ""
	if execErr != nil {
		var coder interface{ ExitCode() int }
		if errors.As(execErr, &coder) {
			gotCode = coder.ExitCode()
			message = execErr.Error()
		} else {
			t.Fatalf("Execute() error = %v (type %T), want ExitCode or nil", execErr, execErr)
		}
	}
	if gotCode != 0 {
		t.Fatalf("exit code = %d, want 0; message=%q", gotCode, message)
	}
	if strings.Contains(message, "unrefined material") {
		t.Fatalf("unexpected consolidation reason: %q", message)
	}
	if stub.calls != 0 {
		t.Fatalf("pressure usecase calls = %d, want 0", stub.calls)
	}

	listed, err := eventUC.List(ctx, apptypes.NewEventListCriteriaBuilder(10).Kind(types.EventKindTranscript).Build())
	if err != nil {
		t.Fatalf("List(transcript) error = %v", err)
	}
	if len(listed) == 0 {
		t.Fatal("expected a transcript row to persist; suppression must not skip recording")
	}
}

func TestHookKimiStop_WorkBasedTrigger(t *testing.T) {
	tests := []struct {
		name         string
		commands     int
		seedBytes    int
		wantExitCode int
	}{
		{name: "work-based due exits 2 with signal work in the ledger", commands: 20, wantExitCode: 2},
		{name: "below min_commands exits 0", commands: 19, wantExitCode: 0},
		{name: "a large body with no commands exits 0", seedBytes: 65 * 1024, wantExitCode: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const sessionID = "session_00000000-0000-4000-8000-000000000001"
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
			t.Setenv("TRACEARY_WORKSPACE", "github.com/dogfood/test")
			cli.SetUserHomeDirFunc(func() (string, error) { return home, nil })
			t.Cleanup(cli.ResetUserHomeDirFunc)
			seedKimiSession(t, home, sessionID, []string{
				`{"type":"metadata","protocol_version":"1.4","created_at":1784466738324}`,
				`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"text","text":"kimi work-based probe"}}}`,
			})

			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "traceary.db")
			db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
			eventDS := sqliteinfra.NewEventDatasource(db)
			sessionDS := sqliteinfra.NewSessionDatasource(db)
			refinementDS := sqliteinfra.NewSessionRefinementDatasource(db)
			ledger := sqliteinfra.NewConsolidationRequestDatasource(db)
			storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
			if err := storeUC.Initialize(ctx); err != nil {
				t.Fatalf("Initialize() error = %v", err)
			}
			base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
			session := model.NewSession(types.SessionID(sessionID), base, "cli", "kimi", "ws")
			start, err := model.NewEventWithClock(
				"evt-start", types.EventKindSessionStarted, "cli", "kimi",
				types.SessionID(sessionID), "ws", "start",
				fixedClock{at: base},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := sessionDS.SaveBoundary(ctx, session, start); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < tt.commands; i++ {
				cmd, err := model.NewEventWithClock(
					types.EventID("evt-cmd-"+strconv.Itoa(i)), types.EventKindCommandExecuted, "cli", "kimi",
					types.SessionID(sessionID), "ws", "cmd",
					fixedClock{at: base.Add(time.Duration(i+2) * time.Second)},
				)
				if err != nil {
					t.Fatal(err)
				}
				if err := eventDS.Save(ctx, cmd); err != nil {
					t.Fatal(err)
				}
			}
			if tt.seedBytes > 0 {
				note, err := model.NewEventWithClock(
					"evt-heavy", types.EventKindNote, "cli", "kimi",
					types.SessionID(sessionID), "ws", strings.Repeat("x", tt.seedBytes),
					fixedClock{at: base.Add(time.Second)},
				)
				if err != nil {
					t.Fatal(err)
				}
				if err := eventDS.Save(ctx, note); err != nil {
					t.Fatal(err)
				}
			}

			rootCmd := cli.NewRootCLI(
				cli.WithStoreManagement(storeUC),
				cli.WithEvent(usecase.NewEventUsecase(eventDS, eventDS)),
				cli.WithConsolidationPressure(usecase.NewConsolidationPressureUsecase(eventDS, refinementDS, sessionDS, ledger)),
				cli.WithConsolidationRequest(usecase.NewConsolidationRequestUsecase(ledger, types.SystemClock{})),
				cli.WithSessionEventOrder(eventDS),
				cli.WithDatabasePathSetter(db.SetPath),
			).Command()
			rootCmd.SetIn(strings.NewReader(readKimiFixture(t, "stop.json")))
			rootCmd.SetOut(&bytes.Buffer{})
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs([]string{"hook", "kimi", "stop", "--db-path", dbPath})
			execErr := rootCmd.Execute()
			gotCode := 0
			message := ""
			if execErr != nil {
				var coder interface{ ExitCode() int }
				if errors.As(execErr, &coder) {
					gotCode = coder.ExitCode()
					message = execErr.Error()
				} else {
					t.Fatalf("Execute() error = %v", execErr)
				}
			}
			if gotCode != tt.wantExitCode {
				t.Fatalf("exit = %d, want %d; %s", gotCode, tt.wantExitCode, message)
			}
			if tt.wantExitCode == 2 {
				if !strings.Contains(message, "unrefined material") {
					t.Fatalf("reason %q missing unrefined material", message)
				}
				row := mustConsolidationRow(t, dbPath, sessionID)
				if row.signal != "work" || row.threshold != 20 || row.pressure < 20 {
					t.Fatalf("ledger = %+v", row)
				}
			}
		})
	}
}

func TestLoadConfig_ConsolidationPolicy(t *testing.T) {
	tests := []struct {
		name            string
		json            string
		unreadable      bool
		danglingSymlink bool
		wantMin         int64
		wantCadence     int64
		wantBytes       int64
		wantBytesSet    bool
	}{
		{
			name:        "absent config file resolves work-based defaults",
			wantMin:     presentation.DefaultConsolidationMinCommands,
			wantCadence: presentation.DefaultConsolidationStopCadence,
			wantBytes:   presentation.DefaultConsolidationThresholdBytes,
		},
		{
			name:        "absent keys default min_commands 20 and stop_cadence 8",
			json:        `{}`,
			wantMin:     20,
			wantCadence: 8,
			wantBytes:   presentation.DefaultConsolidationThresholdBytes,
		},
		{
			name:        "explicit min_commands 0 disables",
			json:        `{"consolidation":{"min_commands":0}}`,
			wantMin:     0,
			wantCadence: 8,
			wantBytes:   presentation.DefaultConsolidationThresholdBytes,
		},
		{
			name:        "explicit stop_cadence 0 disables",
			json:        `{"consolidation":{"stop_cadence":0}}`,
			wantMin:     20,
			wantCadence: 0,
			wantBytes:   presentation.DefaultConsolidationThresholdBytes,
		},
		{
			name:         "explicit threshold_bytes is parsed and marked set",
			json:         `{"consolidation":{"threshold_bytes":131072}}`,
			wantMin:      20,
			wantCadence:  8,
			wantBytes:    131072,
			wantBytesSet: true,
		},
		{
			name:        "malformed config file disables the trigger",
			json:        `{invalid`,
			wantMin:     0,
			wantCadence: 0,
			wantBytes:   0,
		},
		{
			name:        "unreadable config file disables the trigger",
			json:        `{"consolidation":{"min_commands":40}}`,
			unreadable:  true,
			wantMin:     0,
			wantCadence: 0,
			wantBytes:   0,
		},
		{
			name:            "dangling config symlink disables the trigger",
			danglingSymlink: true,
			wantMin:         0,
			wantCadence:     0,
			wantBytes:       0,
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
			got := cfg.Consolidation
			if got.MinCommands != tt.wantMin || got.StopCadence != tt.wantCadence || got.ThresholdBytes != tt.wantBytes || got.ThresholdBytesSet != tt.wantBytesSet {
				t.Fatalf("Consolidation = %+v, want min=%d cadence=%d bytes=%d set=%v",
					got, tt.wantMin, tt.wantCadence, tt.wantBytes, tt.wantBytesSet)
			}
		})
	}
}

func writeConsolidationConfig(t *testing.T, home, consolidationJSON string) {
	t.Helper()
	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"consolidation":` + consolidationJSON + `}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }
