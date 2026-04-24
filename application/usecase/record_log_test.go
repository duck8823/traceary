package usecase_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type eventRepositoryStub struct {
	savedEvent *model.Event
	err        error
}

func (s *eventRepositoryStub) Save(_ context.Context, event *model.Event) error {
	s.savedEvent = event
	return s.err
}

func (s *eventRepositoryStub) SaveWithAudit(_ context.Context, event *model.Event, _ *model.CommandAudit) error {
	s.savedEvent = event
	return s.err
}

func TestEventUsecase_Log(t *testing.T) {
	t.Parallel()

	t.Run("saves event successfully", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		got, err := sut.Log(context.Background(),
			"  hello traceary  ",
			types.EventKind(""),
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID("session-1"),
			types.Workspace("duck8823/traceary"),
			apptypes.LogRedaction{},
		)
		if err != nil {
			t.Fatalf("Log() error = %v", err)
		}
		if got == nil {
			t.Fatalf("Log() event is nil")
		}
		if stub.savedEvent == nil {
			t.Fatalf("Save() event is nil")
		}
		if got != stub.savedEvent {
			t.Fatalf("saved event mismatch")
		}
		if got.EventID().String() == "" {
			t.Fatalf("EventID() is empty")
		}
		if diff := cmp.Diff("hello traceary", got.Body()); diff != "" {
			t.Fatalf("Body() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(types.Client("cli"), got.Client()); diff != "" {
			t.Fatalf("Client() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("codex", got.Agent().String()); diff != "" {
			t.Fatalf("Agent() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("session-1", got.SessionID().String()); diff != "" {
			t.Fatalf("SessionID() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(types.Workspace("duck8823/traceary"), got.Workspace()); diff != "" {
			t.Fatalf("Workspace() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("saves event with specified kind", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		got, err := sut.Log(context.Background(),
			"compact summary text",
			types.EventKind("compact_summary"),
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("session-1"),
			types.Workspace("duck8823/traceary"),
			apptypes.LogRedaction{},
		)
		if err != nil {
			t.Fatalf("Log() error = %v", err)
		}
		if diff := cmp.Diff("compact_summary", got.Kind().String()); diff != "" {
			t.Fatalf("Kind() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("defaults to note when kind is empty", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		got, err := sut.Log(context.Background(),
			"hello",
			types.EventKind(""),
			types.Client("cli"),
			types.Agent("manual"),
			types.SessionID("session-1"),
			types.Workspace(""),
			apptypes.LogRedaction{},
		)
		if err != nil {
			t.Fatalf("Log() error = %v", err)
		}
		if diff := cmp.Diff("note", got.Kind().String()); diff != "" {
			t.Fatalf("Kind() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns error for invalid kind", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		_, err := sut.Log(context.Background(),
			"hello",
			types.EventKind("invalid_kind"),
			types.Client("cli"),
			types.Agent("manual"),
			types.SessionID("session-1"),
			types.Workspace(""),
			apptypes.LogRedaction{},
		)
		if err == nil {
			t.Fatalf("Log() error = nil, want error for invalid kind")
		}
	})

	t.Run("transcript kind applies builtin + extra redactors inside the usecase", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		cfg := apptypes.NewLogRedactionBuilder().
			ExtraRedactPatterns([]string{`my_custom_secret=\S+`}).
			Build()
		got, err := sut.Log(context.Background(),
			"Authorization: Bearer abc.DEF-123 and my_custom_secret=s3cr3tValue42 follows",
			types.EventKindTranscript,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("session-1"),
			types.Workspace("duck8823/traceary"),
			cfg,
		)
		if err != nil {
			t.Fatalf("Log() error = %v", err)
		}
		body := got.Body()
		if strings.Contains(body, "s3cr3tValue42") {
			t.Errorf("transcript body leaked operator extra pattern: %q", body)
		}
		if strings.Contains(body, "abc.DEF-123") {
			t.Errorf("transcript body leaked Bearer token (builtin redactor regression): %q", body)
		}
		if !strings.Contains(body, "[REDACTED]") {
			t.Errorf("transcript body missing [REDACTED] placeholder: %q", body)
		}
	})

	t.Run("non-transcript kind passes body through untouched even with patterns", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		cfg := apptypes.NewLogRedactionBuilder().
			ExtraRedactPatterns([]string{`my_custom_secret=\S+`}).
			Build()
		// Prompt and compact_summary bodies intentionally keep operator
		// intent verbatim; the kind-aware gate inside EventUsecase.Log
		// must not apply redaction to those kinds.
		got, err := sut.Log(context.Background(),
			"my_custom_secret=keepme",
			types.EventKindPrompt,
			types.Client("hook"),
			types.Agent("claude"),
			types.SessionID("session-1"),
			types.Workspace("duck8823/traceary"),
			cfg,
		)
		if err != nil {
			t.Fatalf("Log() error = %v", err)
		}
		if diff := cmp.Diff("my_custom_secret=keepme", got.Body()); diff != "" {
			t.Fatalf("Body() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns error for invalid required fields", func(t *testing.T) {
		t.Parallel()

		stub := &eventRepositoryStub{}
		sut := usecase.NewEventUsecase(stub, nil)

		_, err := sut.Log(context.Background(),
			"hello",
			types.EventKind(""),
			types.Client(""),
			types.Agent(""),
			types.SessionID("session-1"),
			types.Workspace(""),
			apptypes.LogRedaction{},
		)
		if err == nil {
			t.Fatalf("Log() error = nil, want error")
		}
		if stub.savedEvent != nil {
			t.Fatalf("Save() should not be called")
		}
	})
}

// TestEventUsecase_Log_StampsSourceHookFromContext regression guard
// for #672: when the caller threads WithSourceHook through ctx, the
// persisted Event carries the tag so downstream queries can tell a
// Claude `Stop` apart from a Gemini `AfterAgent` (both produce
// transcript kind) without parsing the body string.
func TestEventUsecase_Log_StampsSourceHookFromContext(t *testing.T) {
	t.Parallel()

	stub := &eventRepositoryStub{}
	sut := usecase.NewEventUsecase(stub, nil)

	ctx := apptypes.WithSourceHook(context.Background(), "stop")
	got, err := sut.Log(ctx,
		"transcript body",
		types.EventKindTranscript,
		types.Client("hook"),
		types.Agent("claude"),
		types.SessionID("session-1"),
		types.Workspace("github.com/example/repo"),
		apptypes.LogRedaction{},
	)
	if err != nil {
		t.Fatalf("Log() error = %v", err)
	}
	if got.SourceHook() != "stop" {
		t.Errorf("returned event SourceHook() = %q, want %q", got.SourceHook(), "stop")
	}
	if stub.savedEvent.SourceHook() != "stop" {
		t.Errorf("saved event SourceHook() = %q, want %q", stub.savedEvent.SourceHook(), "stop")
	}
}

// TestEventUsecase_Log_LeavesSourceHookEmptyWithoutContext asserts
// that non-hook writers (CLI `traceary log`, MCP `add_log`) leave
// source_hook empty — the DB column stays NULL and queries can
// cleanly filter hook-driven vs user-driven writes.
func TestEventUsecase_Log_LeavesSourceHookEmptyWithoutContext(t *testing.T) {
	t.Parallel()

	stub := &eventRepositoryStub{}
	sut := usecase.NewEventUsecase(stub, nil)

	got, err := sut.Log(context.Background(),
		"manual note",
		types.EventKindNote,
		types.Client("cli"),
		types.Agent("manual"),
		types.SessionID("session-1"),
		types.Workspace("github.com/example/repo"),
		apptypes.LogRedaction{},
	)
	if err != nil {
		t.Fatalf("Log() error = %v", err)
	}
	if got.SourceHook() != "" {
		t.Errorf("SourceHook() = %q, want empty for non-hook ctx", got.SourceHook())
	}
}
