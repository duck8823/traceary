package queryservice_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type latestSessionFinderStub struct {
	receivedPath  string
	receivedInput queryservice.FindLatestSessionInput
	event         *model.Event
	err           error
}

func (s *latestSessionFinderStub) FindLatestSessionStartedEvent(
	_ context.Context,
	dbPath string,
	input queryservice.FindLatestSessionInput,
) (*model.Event, error) {
	s.receivedPath = dbPath
	s.receivedInput = input
	return s.event, s.err
}

func TestFindLatestSessionQueryService_Run(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDOf("event-1")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-1")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}

	t.Run("直近セッションイベントを返す", func(t *testing.T) {
		t.Parallel()

		stub := &latestSessionFinderStub{
			event: model.EventOf(
				eventID,
				types.EventKindSessionStarted,
				"cli",
				agent,
				sessionID,
				"github.com/duck8823/traceary",
				"session started",
				time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
			),
		}
		sut := queryservice.NewFindLatestSessionQueryService(stub)

		got, err := sut.Run(context.Background(), "/tmp/traceary.db", queryservice.FindLatestSessionInput{
			Client:     "cli",
			Agent:      "codex",
			Repo:       "github.com/duck8823/traceary",
			ActiveOnly: true,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if stub.receivedPath != "/tmp/traceary.db" {
			t.Fatalf("received path = %q, want %q", stub.receivedPath, "/tmp/traceary.db")
		}
		if stub.receivedInput.Agent != "codex" {
			t.Fatalf("received agent = %q, want %q", stub.receivedInput.Agent, "codex")
		}
		if !stub.receivedInput.ActiveOnly {
			t.Fatalf("received activeOnly = %t, want true", stub.receivedInput.ActiveOnly)
		}
		if got.SessionID() != sessionID {
			t.Fatalf("SessionID() = %q, want %q", got.SessionID(), sessionID)
		}
	})

	t.Run("not found エラーは追加で wrap しない", func(t *testing.T) {
		t.Parallel()

		stub := &latestSessionFinderStub{
			err: queryservice.ErrActiveSessionNotFound,
		}
		sut := queryservice.NewFindLatestSessionQueryService(stub)

		_, err := sut.Run(context.Background(), "/tmp/traceary.db", queryservice.FindLatestSessionInput{
			ActiveOnly: true,
		})
		if !errors.Is(err, queryservice.ErrActiveSessionNotFound) {
			t.Fatalf("Run() error = %v, want ErrActiveSessionNotFound", err)
		}
		if err.Error() != queryservice.ErrActiveSessionNotFound.Error() {
			t.Fatalf("error = %q, want %q", err.Error(), queryservice.ErrActiveSessionNotFound.Error())
		}
	})
}
