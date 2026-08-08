package sqlite

import (
	"context"
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

// TestListJoinDoesNotSurfaceEncodedCommandPayloads pins the #1675 correction:
// list/search JOINs only fixed-size audit metadata. Codec-managed payloads
// must go through hydrateAuditPayload so non-identity frames (#1618) do not
// break sensitive-path classification or MCP body rendering.
func TestListJoinDoesNotSurfaceEncodedCommandPayloads(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "encoded-audit.db")
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
		eventID      = "evt-encoded-sensitive"
		sessionID    = "session-encoded"
		plainCommand = "cat /home/user/.ssh/id_rsa"
		plainInput   = "stdin-secret-path"
		plainOutput  = "-----BEGIN OPENSSH PRIVATE KEY-----"
	)
	commandPayload := mustEncodeAuditPayload(t, plainCommand, payloadCodecZstd)
	inputPayload := mustEncodeAuditPayload(t, plainInput, payloadCodecZstd)
	outputPayload := mustEncodeAuditPayload(t, plainOutput, payloadCodecZstd)

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
  exit_code, failed, failure_reason,
  command_codec, command_format_version, command_plaintext_bytes, command_encoded_bytes, command_sha256,
  input_codec, input_format_version, input_plaintext_bytes, input_encoded_bytes, input_sha256,
  output_codec, output_format_version, output_plaintext_bytes, output_encoded_bytes, output_sha256
) VALUES(
  ?, ?, '', 'cat', ?, ?,
  0, 0, ?, ?,
  0, 0, 'none',
  ?, ?, ?, ?, ?,
  ?, ?, ?, ?, ?,
  ?, ?, ?, ?, ?
)`,
		eventID, commandPayload.Bytes, inputPayload.Bytes, outputPayload.Bytes,
		len(plainInput), len(plainOutput),
		commandPayload.Codec, commandPayload.FormatVersion, commandPayload.PlaintextBytes, commandPayload.StoredBytes, commandPayload.SHA256,
		inputPayload.Codec, inputPayload.FormatVersion, inputPayload.PlaintextBytes, inputPayload.StoredBytes, inputPayload.SHA256,
		outputPayload.Codec, outputPayload.FormatVersion, outputPayload.PlaintextBytes, outputPayload.StoredBytes, outputPayload.SHA256,
	); err != nil {
		t.Fatalf("insert command audit: %v", err)
	}

	// Sanity: the physical column holds a zstd frame, not the plaintext a
	// reader would want. Compare exactly rather than by substring — zstd stores
	// a short incompressible input as a raw literal block, so the plaintext is
	// still visible inside the frame and a substring check would report "not
	// encoded" on a correctly encoded payload.
	var storedCommand []byte
	if err := raw.QueryRowContext(ctx, `SELECT command_text FROM command_audits WHERE event_id=?`, eventID).Scan(&storedCommand); err != nil {
		t.Fatalf("read physical command_text: %v", err)
	}
	if string(storedCommand) == plainCommand {
		t.Fatalf("physical command_text still plaintext; test setup is not encoded")
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

	t.Run("command-only hydration supplies list/MCP body plaintext under non-identity codec", func(t *testing.T) {
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
		// Encoded physical bytes must never be returned as the command line.
		if strings.Contains(hydrated.Command(), string(storedCommand)) {
			t.Fatalf("hydrated command contains physical encoded bytes")
		}
		if strings.TrimSpace(hydrated.Command()) == "" {
			t.Fatal("unexpected empty command after hydration")
		}
		// List body surface uses audit.Command() after hydration; zstd must
		// decode to the full line, not leave the command_name basename.
		if strings.TrimSpace(fresh[0].Body()) != "" {
			t.Fatalf("events.body should stay empty after #1675, got %q", fresh[0].Body())
		}
	})
}

func mustEncodeAuditPayload(t *testing.T, plaintext, codec string) encodedPayload {
	t.Helper()
	payload, err := encodePayload([]byte(plaintext), codec)
	if err != nil {
		t.Fatalf("encodePayload(%q): %v", codec, err)
	}
	return payload
}
