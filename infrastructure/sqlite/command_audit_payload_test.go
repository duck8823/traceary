package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/sensitivepath"
	"github.com/duck8823/traceary/domain/types"
)

// TestListJoinDoesNotSurfaceCommandPayloads pins the #1675 correction:
// list/search JOINs only fixed-size audit metadata. Payloads must go through
// hydrateAuditPayload so sensitive-path classification and MCP body rendering
// see the stored plaintext only after an explicit hydrate.
func TestListJoinDoesNotSurfaceCommandPayloads(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "audit-payload.db")
	database := NewDatabase(path, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := database.initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	raw, err := database.open(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.release(raw)

	const (
		eventID      = "evt-sensitive"
		sessionID    = "session-payload"
		plainCommand = "cat /home/user/.ssh/id_rsa"
		plainInput   = "stdin-secret-path"
		plainOutput  = "-----BEGIN OPENSSH PRIVATE KEY-----"
	)

	if _, err := raw.ExecContext(ctx, `
INSERT INTO events(id, kind, client, agent, session_id, workspace, body, body_availability, created_at, source_hook)
VALUES(?, 'command_executed', 'cli', 'codex', ?, '/repo', '', 'available', ?, '')`,
		eventID, sessionID, time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `
INSERT INTO command_audits(
  event_id, command_text, command_wrapper, command_name, input_text, output_text,
  input_truncated, output_truncated, input_original_bytes, output_original_bytes,
  exit_code, failed, failure_reason
) VALUES(
  ?, ?, '', 'cat', ?, ?,
  0, 0, ?, ?,
  0, 0, 'none'
)`,
		eventID, plainCommand, plainInput, plainOutput,
		len(plainInput), len(plainOutput),
	); err != nil {
		t.Fatalf("insert command audit: %v", err)
	}

	datasource := NewEventDatasource(database)
	listed, err := datasource.ListRecent(ctx, 10, 0, types.EventKindCommandExecuted, "", "", sessionID, "", false, time.Time{}, time.Time{}, "")
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListRecent len = %d, want 1", len(listed))
	}
	audit, ok := listed[0].CommandAudit().Value()
	if !ok || audit == nil {
		t.Fatal("listed event missing command audit metadata")
	}
	if diff := cmp.Diff("cat", audit.CommandIdentity().Command().String()); diff != "" {
		t.Fatalf("command_name mismatch (-want +got):\n%s", diff)
	}
	if audit.Command() != "" || audit.Input() != "" || audit.Output() != "" {
		t.Fatalf("list join leaked payload fields: command=%q input=%q output=%q", audit.Command(), audit.Input(), audit.Output())
	}
	// Without hydration, sensitive classification must not see encoded bytes
	// as a false-negative match surface either — empty payloads yield no match.
	if sensitivepath.Classify(sensitivepath.Input{Command: audit.Command(), Input: audit.Input(), Output: audit.Output()}).Matched {
		t.Fatal("metadata-only audit unexpectedly matched sensitive path")
	}

	t.Run("full payload hydration restores sensitive classification", func(t *testing.T) {
		if err := datasource.HydrateCommandAudits(ctx, listed, queryservice.FullCommandAuditPayload()); err != nil {
			t.Fatalf("HydrateCommandAudits(full): %v", err)
		}
		hydrated, ok := listed[0].CommandAudit().Value()
		if !ok || hydrated == nil {
			t.Fatal("missing hydrated audit")
		}
		if diff := cmp.Diff(plainCommand, hydrated.Command()); diff != "" {
			t.Fatalf("command mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(plainInput, hydrated.Input()); diff != "" {
			t.Fatalf("input mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(plainOutput, hydrated.Output()); diff != "" {
			t.Fatalf("output mismatch (-want +got):\n%s", diff)
		}
		cls := sensitivepath.Classify(sensitivepath.Input{
			Command: hydrated.Command(),
			Input:   hydrated.Input(),
			Output:  hydrated.Output(),
		})
		if !cls.Matched {
			t.Fatalf("expected sensitive match after hydration, got %#v", cls)
		}
	})

	t.Run("command-only hydration supplies list/MCP body plaintext", func(t *testing.T) {
		// Re-list so this subtest starts from metadata-only state.
		// CLI list/search/tail/context and MCP list/search/context all use
		// CommandOnlyPayload() so eventBodyForDisplay / MCP body can show the
		// full command line while events.body stays empty (#1675).
		fresh, err := datasource.ListRecent(ctx, 10, 0, types.EventKindCommandExecuted, "", "", sessionID, "", false, time.Time{}, time.Time{}, "")
		if err != nil {
			t.Fatalf("ListRecent: %v", err)
		}
		// Pre-hydrate: body empty and command_name only must not be treated as
		// the display line (basename "cat" is not the command that ran).
		pre, ok := fresh[0].CommandAudit().Value()
		if !ok || pre == nil {
			t.Fatal("missing pre-hydrate audit")
		}
		if pre.Command() != "" {
			t.Fatalf("pre-hydrate command should be empty, got %q", pre.Command())
		}
		if diff := cmp.Diff("cat", pre.CommandIdentity().Command().String()); diff != "" {
			t.Fatalf("command_name mismatch (-want +got):\n%s", diff)
		}

		if err := datasource.HydrateCommandAudits(ctx, fresh, queryservice.CommandOnlyPayload()); err != nil {
			t.Fatalf("HydrateCommandAudits(command): %v", err)
		}
		hydrated, ok := fresh[0].CommandAudit().Value()
		if !ok || hydrated == nil {
			t.Fatal("missing hydrated audit")
		}
		if diff := cmp.Diff(plainCommand, hydrated.Command()); diff != "" {
			t.Fatalf("command mismatch (-want +got):\n%s", diff)
		}
		// Input/output stay unhydrated on the command-only path.
		if hydrated.Input() != "" || hydrated.Output() != "" {
			t.Fatalf("command-only hydration filled I/O: input=%q output=%q", hydrated.Input(), hydrated.Output())
		}
		if strings.TrimSpace(hydrated.Command()) == "" {
			t.Fatal("unexpected empty command after hydration")
		}
		if strings.TrimSpace(fresh[0].Body()) != "" {
			t.Fatalf("events.body should stay empty after #1675, got %q", fresh[0].Body())
		}
	})
}

// TestHydrateCommandAuditsQueryCount pins the O(1) batch hydration contract:
// for a page of N events, HydrateCommandAudits must issue exactly 1 schema
// probe and 1 batch SELECT — not O(N) or O(N × fields) queries.
func TestHydrateCommandAuditsQueryCount(t *testing.T) {
	t.Parallel()

	const pageSize = 25
	const sessionID = "session-qcount"

	t.Run("plaintext shape emits O(1) queries for N events", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		path := filepath.Join(t.TempDir(), "qcount-plaintext.db")
		database := NewDatabase(path, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
		if err := database.initialize(ctx); err != nil {
			t.Fatalf("initialize: %v", err)
		}
		raw, err := database.open(ctx)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer database.release(raw)

		for i := range pageSize {
			eventID := fmt.Sprintf("evt-qcount-plain-%02d", i)
			if _, err := raw.ExecContext(ctx, `
INSERT INTO events(id, kind, client, agent, session_id, workspace, body, body_availability, created_at, source_hook)
VALUES(?, 'command_executed', 'cli', 'claude', ?, '/repo', '', 'available', ?, '')`,
				eventID, sessionID,
				time.Date(2026, 8, 9, 12, 0, i, 0, time.UTC).Format(time.RFC3339Nano),
			); err != nil {
				t.Fatalf("insert event %d: %v", i, err)
			}
			if _, err := raw.ExecContext(ctx, `
INSERT INTO command_audits(
  event_id, command_text, command_wrapper, command_name, input_text, output_text,
  input_truncated, output_truncated, input_original_bytes, output_original_bytes,
  exit_code, failed, failure_reason
) VALUES(
  ?, ?, '', 'ls', '', '',
  0, 0, 0, 0,
  0, 0, 'none'
)`,
				eventID, "ls -la",
			); err != nil {
				t.Fatalf("insert command_audit %d: %v", i, err)
			}
		}

		datasource := NewEventDatasource(database)
		listed, err := datasource.ListRecent(ctx, pageSize, 0, types.EventKindCommandExecuted, "", "", sessionID, "", false, time.Time{}, time.Time{}, "")
		if err != nil {
			t.Fatalf("ListRecent: %v", err)
		}
		if len(listed) != pageSize {
			t.Fatalf("ListRecent len = %d, want %d", len(listed), pageSize)
		}

		var queryKinds []string
		datasource.SetAuditHydrationQueryHookForTest(func(kind string) {
			queryKinds = append(queryKinds, kind)
		})

		if err := datasource.HydrateCommandAudits(ctx, listed, queryservice.CommandOnlyPayload()); err != nil {
			t.Fatalf("HydrateCommandAudits: %v", err)
		}

		// Exactly 2 queries: one schema probe + one batch SELECT.
		if len(queryKinds) != 2 {
			t.Errorf("expected 2 queries (schema + payload), got %d: %v", len(queryKinds), queryKinds)
		}
		if len(queryKinds) >= 1 && queryKinds[0] != "schema" {
			t.Errorf("first query kind = %q, want %q", queryKinds[0], "schema")
		}
		if len(queryKinds) >= 2 && queryKinds[1] != "payload" {
			t.Errorf("second query kind = %q, want %q", queryKinds[1], "payload")
		}

		// Correctness: all events must have stored command payloads.
		for i, ev := range listed {
			audit, ok := ev.CommandAudit().Value()
			if !ok || audit == nil {
				t.Errorf("event[%d]: missing audit after hydration", i)
				continue
			}
			if audit.Command() != "ls -la" {
				t.Errorf("event[%d]: command = %q, want %q", i, audit.Command(), "ls -la")
			}
		}
	})

	t.Run("legacy shape emits O(1) queries for N events", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		path := filepath.Join(t.TempDir(), "qcount-legacy.db")
		database := NewDatabase(path, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
		if err := database.initialize(ctx); err != nil {
			t.Fatalf("initialize: %v", err)
		}
		raw, err := database.open(ctx)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer database.release(raw)

		for i := range pageSize {
			eventID := fmt.Sprintf("evt-qcount-legacy-%02d", i)
			if _, err := raw.ExecContext(ctx, `
INSERT INTO events(id, kind, client, agent, session_id, workspace, body, body_availability, created_at, source_hook)
VALUES(?, 'command_executed', 'cli', 'claude', ?, '/repo', '', 'available', ?, '')`,
				eventID, sessionID,
				time.Date(2026, 8, 9, 12, 0, i, 0, time.UTC).Format(time.RFC3339Nano),
			); err != nil {
				t.Fatalf("insert event %d: %v", i, err)
			}
			if _, err := raw.ExecContext(ctx, `
INSERT INTO command_audits(
  event_id, command_text, command_wrapper, command_name, input_text, output_text,
  input_truncated, output_truncated, input_original_bytes, output_original_bytes,
  exit_code, failed, failure_reason
) VALUES(?, 'ls -la', '', 'ls', '', '', 0, 0, 0, 0, 0, 0, 'none')`,
				eventID,
			); err != nil {
				t.Fatalf("insert command_audit %d: %v", i, err)
			}
		}

		datasource := NewEventDatasource(database)
		listed, err := datasource.ListRecent(ctx, pageSize, 0, types.EventKindCommandExecuted, "", "", sessionID, "", false, time.Time{}, time.Time{}, "")
		if err != nil {
			t.Fatalf("ListRecent: %v", err)
		}
		if len(listed) != pageSize {
			t.Fatalf("ListRecent len = %d, want %d", len(listed), pageSize)
		}

		var queryKinds []string
		datasource.SetAuditHydrationQueryHookForTest(func(kind string) {
			queryKinds = append(queryKinds, kind)
		})

		if err := datasource.HydrateCommandAudits(ctx, listed, queryservice.CommandOnlyPayload()); err != nil {
			t.Fatalf("HydrateCommandAudits: %v", err)
		}

		// Exactly 2 queries: one schema probe + one batch SELECT.
		if len(queryKinds) != 2 {
			t.Errorf("expected 2 queries (schema + payload), got %d: %v", len(queryKinds), queryKinds)
		}
	})
}
