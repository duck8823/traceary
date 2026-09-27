package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestLogOnlyPublicStartJSONAndEndOccurrenceContracts(t *testing.T) {
	fx := newEnvelopeFixture(t, "other-fixture", "codex", 0)
	run := func(extra ...string) (string, error) {
		root := cli.NewRootCLI(cli.WithStoreManagement(fx.storeUC), cli.WithSession(fx.sessionUC), cli.WithDatabasePathSetter(fx.db.SetPath)).Command()
		out := &bytes.Buffer{}
		root.SetOut(out)
		root.SetErr(&bytes.Buffer{})
		root.SetArgs(append(extra, "--db-path", fx.dbPath, "--session-id", "public-group", "--client", "cli", "--agent", "codex", "--workspace", "public-workspace"))
		err := root.Execute()
		return out.String(), err
	}
	first, err := run("session", "start", "--json")
	if err != nil {
		t.Fatal(err)
	}
	again, err := run("session", "start", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var initial, repeated map[string]any
	if err := json.Unmarshal([]byte(first), &initial); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(again), &repeated); err != nil {
		t.Fatal(err)
	}
	if initial["event_id"] == nil || repeated["event_id"] != initial["event_id"] || repeated["created_at"] != initial["created_at"] {
		t.Fatal("JSON registration did not return real original event")
	}
	id, err := run("session", "start", "--id-only")
	if err != nil || id != "public-group\n" {
		t.Fatalf("id output=%q err=%v", id, err)
	}
	end1, err := run("session", "end", "--json")
	if err != nil {
		t.Fatal(err)
	}
	end2, err := run("session", "end", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var a, b map[string]any
	if err := json.Unmarshal([]byte(end1), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(end2), &b); err != nil {
		t.Fatal(err)
	}
	if a["event_id"] == b["event_id"] || a["kind"] != "session_ended" {
		t.Fatal("distinct explicit end output collapsed")
	}
	starts, err := fx.eventDS.ListRecent(context.Background(), 10, 0, types.EventKindSessionStarted, "", "", "public-group", "", false, time.Time{}, time.Time{}, "")
	if err != nil || len(starts) != 1 {
		t.Fatalf("starts=%d err=%v", len(starts), err)
	}
}
