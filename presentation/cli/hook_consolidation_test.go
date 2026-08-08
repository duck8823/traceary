package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

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
	_ int64,
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
		seedBytes         int    // body bytes strictly after covers_to (or whole session)
		coveredBytes      int    // body on the covers_to boundary event (excluded from pressure)
		withRefinement    bool
		pressureErr       error // when set, injects a stub that errors instead of real store
		recordErr         error // when set, transcript Log fails; consolidation must not fire
		noExtractableText bool  // omit assistant text so extractor fail-softs (recorded=false)
		unresolvedSession bool  // force session resolve to empty (recorded=false, err=nil)
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
			name:          "65 KiB unrefined exits 2 and names the session",
			client:        "claude",
			seedBytes:     65 * 1024,
			wantExitCode:  2,
			wantStderrSub: []string{sessionID, "unrefined material"},
		},
		{
			name:           "previous refinement summary and covers_to appear in reason",
			client:         "codex",
			seedBytes:      65 * 1024,
			withRefinement: true,
			wantExitCode:   2,
			wantStderrSub:  []string{sessionID, "previous fold summary", "covers_to=evt-cover"},
		},
		{
			name:          "threshold 0 never fires",
			client:        "claude",
			thresholdJSON: "0",
			seedBytes:     65 * 1024,
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
			seedBytes:    65 * 1024,
			wantExitCode: 0,
			wantNoFire:   true,
		},
		{
			name:         "antigravity never fires",
			client:       "antigravity",
			seedBytes:    65 * 1024,
			wantExitCode: 0,
			wantNoFire:   true,
		},
		{
			name:           "events at or before covers_to are excluded from pressure",
			client:         "kimi",
			coveredBytes:   65 * 1024, // would fire if included
			seedBytes:      0,         // nothing after covers_to
			withRefinement: true,
			wantExitCode:   0,
		},
		{
			// Recording fails inside runHookBestEffort; payload must stay unset so
			// pressure is never measured against a turn that was not stored.
			name:         "recording failure does not request consolidation when pressure is over threshold",
			client:       "codex",
			seedBytes:    65 * 1024,
			recordErr:    errors.New("disk full"),
			wantExitCode: 0,
			wantNoFire:   true,
		},
		{
			// Fail-soft skip (recorded=false, err=nil): resolvable session but
			// no extractable assistant text. err==nil must not set payload or
			// request consolidation even when unrefined material is already
			// over the threshold.
			name:              "no extractable content does not request consolidation when pressure is over threshold",
			client:            "codex",
			seedBytes:         65 * 1024,
			noExtractableText: true,
			wantExitCode:      0,
			wantNoFire:        true,
		},
		{
			// Fail-soft skip (recorded=false, err=nil): extractable content but
			// session resolution yields nothing. Same gate as above — pressure
			// must not run against a turn that was never persisted.
			name:              "unresolved session does not request consolidation when pressure is over threshold",
			client:            "codex",
			seedBytes:         65 * 1024,
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
					result: usecase.ConsolidationPressureResult{PressureBytes: int64(tt.seedBytes), Due: true},
				}
				pressureUC = stub
			} else {
				pressureUC = usecase.NewConsolidationPressureUsecase(eventDS, refinementDS)
			}

			// Transcript capture needs a payload the client extractor accepts.
			// Codex last_assistant_message is the simplest cross-client path
			// that still resolves session_id from the JSON body.
			// Claude stop payload uses transcript_path; for clients without a
			// message field the durable path fail-softs. With a successful
			// record path, consolidation still runs on the session_id.
			payload := `{"session_id":"` + sessionID + `","cwd":"/tmp","last_assistant_message":"ok","prompt_response":"ok"}`
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

// TestHookKimiStop_ConsolidationPressure pins the native Kimi Stop path
// (`traceary hook kimi stop`) to the same consolidation gate as
// `hook transcript`: exit 2 only when a turn was recorded on this firing
// and pressure is over threshold. Idempotent redeliveries that write nothing
// must stay exit 0 even when pressure remains due (#1674 / #1711).
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
	// Unrefined material already over the default 64 KiB threshold so the
	// pressure check is Due as soon as a transcript row lands.
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

	// Stub reports Due so the test does not depend on how large the just-
	// recorded transcript body is relative to the seed; the gate under test
	// is "was a row written on this firing?", not the pressure arithmetic.
	stub := &consolidationPressureStub{
		result: usecase.ConsolidationPressureResult{
			PressureBytes: 65 * 1024,
			Due:           true,
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
		for _, sub := range []string{sessionID, "unrefined material"} {
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

func TestLoadConfig_ConsolidationThreshold(t *testing.T) {
	tests := []struct {
		name            string
		json            string // empty means do not write a config file
		unreadable      bool
		danglingSymlink bool // config.json is a symlink whose target is gone
		want            int64
	}{
		{
			name: "absent config file resolves the 64 KiB default",
			want: presentation.DefaultConsolidationThresholdBytes,
		},
		{
			name: "absent key defaults to 64 KiB",
			json: `{}`,
			want: presentation.DefaultConsolidationThresholdBytes,
		},
		{
			name: "explicit zero disables",
			json: `{"consolidation":{"threshold_bytes":0}}`,
			want: 0,
		},
		{
			name: "custom threshold is honoured",
			json: `{"consolidation":{"threshold_bytes":131072}}`,
			want: 131072,
		},
		{
			name: "malformed config file resolves threshold 0",
			json: `{invalid`,
			want: 0,
		},
		{
			// A broken-but-present file must not re-enable a trigger the
			// operator may have set to 0; disable rather than default.
			name:       "unreadable config file resolves threshold 0",
			json:       `{"consolidation":{"threshold_bytes":131072}}`,
			unreadable: true,
			want:       0,
		},
		{
			// ReadFile follows the link and gets ENOENT, but the directory
			// entry still exists. That is unusable operator intent, not
			// "never configured" — same disable-not-default policy.
			name:            "dangling config symlink resolves threshold 0",
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
				// Point at a sibling that is never created.
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
			if cfg.Consolidation.ThresholdBytes != tt.want {
				t.Fatalf("ThresholdBytes = %d, want %d", cfg.Consolidation.ThresholdBytes, tt.want)
			}
		})
	}
}

func writeConsolidationConfig(t *testing.T, home, threshold string) {
	t.Helper()
	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"consolidation":{"threshold_bytes":` + threshold + `}}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }
