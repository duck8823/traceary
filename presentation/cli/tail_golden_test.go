package cli

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestRunTail_JSON_NDJSONGolden(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	older := mustTailGoldenEvent(t, "event-golden-tail-1", types.EventKindNote, "cli", "codex", "session-golden-tail", "duck8823/traceary", "tail initial older", time.Date(2026, 4, 8, 16, 0, 0, 0, time.UTC), "")
	newer := mustTailGoldenEvent(t, "event-golden-tail-2", types.EventKindPrompt, "hook", "codex", "session-golden-tail", "duck8823/traceary", "tail initial newer", time.Date(2026, 4, 8, 16, 5, 0, 0, time.UTC), "user_prompt_submit")

	eventStub := &tailEventUsecaseStub{
		listResponses: [][]*model.Event{{newer, older}},
		onList: func(callIndex int, _ apptypes.EventListCriteria) {
			if callIndex == 0 {
				cancel()
			}
		},
	}
	stdout := &bytes.Buffer{}
	sut := NewRootCLI(
		WithStoreManagement(tailStoreManagementStub{}),
		WithEvent(eventStub),
	)

	if err := sut.runTail(ctx, &bytes.Buffer{}, stdout, tailCommandInput{
		dbPath:        "/tmp/test-traceary.db",
		limit:         2,
		repo:          "duck8823/traceary",
		asJSON:        true,
		nowFunc:       func() time.Time { return time.Date(2026, 4, 8, 16, 10, 0, 0, time.UTC) },
		tickerFactory: func(time.Duration) tailTicker { return newFakeTailTicker() },
	}); err != nil {
		t.Fatalf("runTail() error = %v", err)
	}

	// Tail's --json mode is NDJSON: each emitted event is one complete JSON
	// object on its own line, with no surrounding array and stable event field
	// order shared with the non-streaming event JSON contract.
	assertTailNDJSONGolden(t, stdout.Bytes(), filepath.Join("testdata", "tail", "initial_multi_event.golden.txt"))
}

func assertTailNDJSONGolden(t *testing.T, got []byte, fixturePath string) {
	t.Helper()

	if shouldUpdateTailGolden() {
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

func shouldUpdateTailGolden() bool {
	updateFlag := flag.Lookup("update")
	return updateFlag != nil && updateFlag.Value.String() == "true"
}

func mustTailGoldenEvent(
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
