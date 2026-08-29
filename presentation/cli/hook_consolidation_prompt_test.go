package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	cli "github.com/duck8823/traceary/presentation/cli"
)

func TestHookPrompt_ConsolidationChannels(t *testing.T) {
	const sessionID = "sess-prompt-channel"

	tests := []struct {
		name               string
		client             string
		commandCount       int
		promptText         *string
		priorRequest       bool
		config             string
		unresolvedSession  bool
		subagent           bool
		pressureStub       bool
		wantEmptyStdout    bool
		wantStdoutJSON     bool
		wantStdoutPrefix   string
		wantLedgerDelivery string
		wantPressureCalls  int
	}{
		{
			name:               "codex over min_commands writes plain-text consolidation context to stdout",
			client:             "codex",
			commandCount:       20,
			wantStdoutPrefix:   "[Traceary] Session " + sessionID,
			wantLedgerDelivery: "additional_context",
		},
		{
			name:               "gemini over min_commands writes exactly one BeforeAgent additionalContext envelope",
			client:             "gemini",
			commandCount:       20,
			wantStdoutJSON:     true,
			wantStdoutPrefix:   "[Traceary] Session " + sessionID,
			wantLedgerDelivery: "additional_context",
		},
		{
			name:            "claude over min_commands writes nothing at prompt time",
			client:          "claude",
			commandCount:    20,
			wantEmptyStdout: true,
		},
		{
			name:            "codex below min_commands writes nothing",
			client:          "codex",
			commandCount:    19,
			wantEmptyStdout: true,
		},
		{
			name:            "gemini below min_commands writes nothing",
			client:          "gemini",
			commandCount:    19,
			wantEmptyStdout: true,
		},
		{
			name:               "codex with an empty prompt still injects when the session id resolves",
			client:             "codex",
			commandCount:       20,
			promptText:         strPtr(""),
			wantStdoutPrefix:   "[Traceary] Session " + sessionID,
			wantLedgerDelivery: "additional_context",
		},
		{
			name:              "an unresolvable session id writes nothing",
			client:            "codex",
			commandCount:      20,
			unresolvedSession: true,
			wantEmptyStdout:   true,
		},
		{
			name:              "min_commands 0 disables the prompt channel",
			client:            "codex",
			commandCount:      20,
			config:            `{"min_commands":0}`,
			pressureStub:      true,
			wantEmptyStdout:   true,
			wantPressureCalls: 0,
		},
		{
			name:              "stop_cadence 0 disables the prompt channel",
			client:            "codex",
			commandCount:      20,
			config:            `{"stop_cadence":0}`,
			pressureStub:      true,
			wantEmptyStdout:   true,
			wantPressureCalls: 0,
		},
		{
			name:            "a request already recorded at this turn suppresses the prompt channel",
			client:          "codex",
			commandCount:    20,
			priorRequest:    true,
			wantEmptyStdout: true,
		},
		{
			name:            "a subagent session is never asked at prompt time",
			client:          "codex",
			commandCount:    20,
			subagent:        true,
			wantEmptyStdout: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
			t.Setenv("TRACEARY_WORKSPACE", "github.com/dogfood/test")
			if tt.config != "" {
				writeConsolidationConfig(t, home, tt.config)
			}

			sid := sessionID
			if tt.subagent {
				sid = "sess-child-prompt"
			}
			fx := newPromptChannelFixture(t, sid, tt.client, tt.commandCount, tt.subagent)
			if tt.priorRequest {
				seedPriorConsolidationRequest(t, fx)
			}
			var stub *consolidationPressureStub
			if tt.pressureStub {
				stub = &consolidationPressureStub{
					result: usecase.ConsolidationPressureResult{Commands: int64(tt.commandCount), Due: true},
				}
				fx.pressureUC = stub
			}
			if tt.unresolvedSession {
				cli.SetResolveHookTranscriptSessionIDFunc(func(_ []byte, _ string) (types.SessionID, error) {
					return "", nil
				})
				t.Cleanup(cli.ResetResolveHookTranscriptSessionIDFunc)
			}

			prompt := "next"
			if tt.promptText != nil {
				prompt = *tt.promptText
			}
			payload := map[string]any{"session_id": sid, "cwd": "/tmp"}
			if prompt != "" {
				payload["prompt"] = prompt
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			stdout, code := fx.runPrompt(t, tt.client, string(raw))
			if code != 0 {
				t.Fatalf("exit = %d, want 0; stdout=%q", code, stdout)
			}
			if stub != nil && stub.calls != tt.wantPressureCalls {
				t.Fatalf("pressure calls = %d, want %d", stub.calls, tt.wantPressureCalls)
			}
			if tt.wantEmptyStdout {
				if stdout != "" {
					t.Fatalf("stdout = %q, want empty", stdout)
				}
				wantRows := 0
				if tt.priorRequest {
					wantRows = 1
				}
				if got := countConsolidationRequests(t, fx.dbPath); got != wantRows {
					t.Fatalf("ledger rows = %d, want %d", got, wantRows)
				}
				return
			}
			if tt.wantStdoutJSON {
				assertGeminiBeforeAgentEnvelope(t, stdout, tt.wantStdoutPrefix)
			} else {
				if json.Valid([]byte(strings.TrimSpace(stdout))) {
					t.Fatalf("codex stdout must not be JSON: %q", stdout)
				}
				if !strings.HasPrefix(stdout, tt.wantStdoutPrefix) {
					t.Fatalf("stdout %q does not start with %q", stdout, tt.wantStdoutPrefix)
				}
				if !strings.Contains(stdout, "traceary session refine") || !strings.Contains(stdout, "--produced-by agent") || !strings.Contains(stdout, "traceary-session-refine") {
					t.Fatalf("stdout missing refine skill/command: %q", stdout)
				}
				if n := strings.Count(stdout, "\n"); n > 3 {
					t.Fatalf("newlines = %d, want <= 3", n)
				}
			}
			row := mustConsolidationRow(t, fx.dbPath, sid)
			if row.delivery != tt.wantLedgerDelivery {
				t.Fatalf("delivery = %q, want %q", row.delivery, tt.wantLedgerDelivery)
			}
		})
	}
}

func TestHookPrompt_LedgerDeliveryIsAdditionalContext(t *testing.T) {
	for _, client := range []string{"codex", "gemini"} {
		t.Run(client, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
			t.Setenv("TRACEARY_WORKSPACE", "github.com/dogfood/test")
			const sessionID = "sess-prompt-delivery"
			fx := newPromptChannelFixture(t, sessionID, client, 20, false)
			payload := `{"session_id":"` + sessionID + `","cwd":"/tmp","prompt":"next"}`
			stdout, code := fx.runPrompt(t, client, payload)
			if code != 0 {
				t.Fatalf("exit = %d, stdout=%q", code, stdout)
			}
			if stdout == "" {
				t.Fatal("expected injection")
			}
			row := mustConsolidationRow(t, fx.dbPath, sessionID)
			if row.delivery != types.ConsolidationDeliveryAdditionalContext.String() {
				t.Fatalf("delivery = %q", row.delivery)
			}
			if row.signal != usecase.ConsolidationSignalWork {
				t.Fatalf("signal = %q, want work", row.signal)
			}
			if _, err := types.ConsolidationDeliveryFrom(row.delivery); err != nil {
				t.Fatalf("ConsolidationDeliveryFrom(%q) = %v", row.delivery, err)
			}
		})
	}
}

func strPtr(v string) *string { return &v }

func assertGeminiBeforeAgentEnvelope(t *testing.T, stdout, wantPrefix string) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	var env struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("decode gemini envelope: %v; stdout=%q", err, stdout)
	}
	if env.HookSpecificOutput.HookEventName != "BeforeAgent" {
		t.Fatalf("hookEventName = %q, want BeforeAgent", env.HookSpecificOutput.HookEventName)
	}
	if !strings.HasPrefix(env.HookSpecificOutput.AdditionalContext, wantPrefix) {
		t.Fatalf("additionalContext = %q, want prefix %q", env.HookSpecificOutput.AdditionalContext, wantPrefix)
	}
	if err := dec.Decode(&map[string]any{}); !errors.Is(err, io.EOF) {
		t.Fatalf("second decode = %v, want EOF (nothing else on stdout)", err)
	}
}

type promptChannelFixture struct {
	sessionID  string
	dbPath     string
	db         *sqliteinfra.Database
	eventDS    *sqliteinfra.EventDatasource
	storeUC    usecase.StoreManagementUsecase
	eventUC    usecase.EventUsecase
	sessionUC  usecase.SessionUsecase
	pressureUC usecase.ConsolidationPressureUsecase
	requestUC  usecase.ConsolidationRequestUsecase
	order      model.SessionEventOrderRepository
	ledger     *sqliteinfra.ConsolidationRequestDatasource
}

func newPromptChannelFixture(t *testing.T, sessionID, agent string, commandCount int, subagent bool) *promptChannelFixture {
	t.Helper()
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
	targetID := types.SessionID(sessionID)
	if subagent {
		parent := model.NewSession("sess-parent-prompt", base, "cli", types.Agent(agent), "ws")
		parentStart, err := model.NewEventWithClock(
			"evt-parent", types.EventKindSessionStarted, "cli", types.Agent(agent),
			parent.SessionID(), "ws", "start", fixedClock{at: base},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := sessionDS.SaveBoundary(ctx, parent, parentStart); err != nil {
			t.Fatal(err)
		}
		child := model.NewChildSession(parent, targetID, base, types.Agent(agent), "ws", "evt-parent", "explore", 1)
		childStart, err := model.NewEventWithClock(
			"evt-child", types.EventKindSessionStarted, "cli", types.Agent(agent),
			targetID, "ws", "start", fixedClock{at: base.Add(time.Second)},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := sessionDS.SaveBoundary(ctx, child, childStart); err != nil {
			t.Fatal(err)
		}
	} else {
		session := model.NewSession(targetID, base, "cli", types.Agent(agent), "ws")
		start, err := model.NewEventWithClock(
			"evt-start", types.EventKindSessionStarted, "cli", types.Agent(agent),
			targetID, "ws", "start", fixedClock{at: base},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := sessionDS.SaveBoundary(ctx, session, start); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < commandCount; i++ {
		cmd, err := model.NewEventWithClock(
			types.EventID("evt-cmd-"+strconv.Itoa(i)), types.EventKindCommandExecuted, "cli", types.Agent(agent),
			targetID, "ws", "cmd", fixedClock{at: base.Add(time.Duration(i+2) * time.Second)},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := eventDS.Save(ctx, cmd); err != nil {
			t.Fatal(err)
		}
	}
	return &promptChannelFixture{
		sessionID:  sessionID,
		dbPath:     dbPath,
		db:         db,
		eventDS:    eventDS,
		storeUC:    storeUC,
		eventUC:    usecase.NewEventUsecase(eventDS, eventDS),
		sessionUC:  usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS, usecase.SessionUsecaseDependencies{}),
		pressureUC: usecase.NewConsolidationPressureUsecase(eventDS, refinementDS, sessionDS, ledger),
		requestUC:  usecase.NewConsolidationRequestUsecase(ledger, types.SystemClock{}),
		order:      eventDS,
		ledger:     ledger,
	}
}

func (fx *promptChannelFixture) runPrompt(t *testing.T, client, payload string) (string, int) {
	t.Helper()
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(fx.storeUC),
		cli.WithEvent(fx.eventUC),
		cli.WithSession(fx.sessionUC),
		cli.WithConsolidationPressure(fx.pressureUC),
		cli.WithConsolidationRequest(fx.requestUC),
		cli.WithSessionEventOrder(fx.order),
		cli.WithDatabasePathSetter(fx.db.SetPath),
	).Command()
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"hook", "prompt", client, "--db-path", fx.dbPath})
	err := rootCmd.Execute()
	if err == nil {
		return stdout.String(), 0
	}
	var coder interface{ ExitCode() int }
	if errors.As(err, &coder) {
		return stdout.String(), coder.ExitCode()
	}
	t.Fatalf("Execute() error = %v (type %T)", err, err)
	return stdout.String(), 0
}

func seedPriorConsolidationRequest(t *testing.T, fx *promptChannelFixture) {
	t.Helper()
	latest := queryLatestEventID(t, fx.dbPath, fx.sessionID)
	if _, err := fx.requestUC.Record(context.Background(), usecase.ConsolidationRequestInput{
		SessionID:      types.SessionID(fx.sessionID),
		Client:         "codex",
		AtEventID:      types.EventID(latest),
		Signal:         usecase.ConsolidationSignalWork,
		PressureValue:  20,
		ThresholdValue: 20,
		Delivery:       types.ConsolidationDeliveryStopExit2,
	}); err != nil {
		t.Fatalf("seed request: %v", err)
	}
}
