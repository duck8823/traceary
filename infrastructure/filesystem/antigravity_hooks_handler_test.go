package filesystem

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// antigravityDoc is a lenient view over a rendered Antigravity hooks.json used
// by the handler tests. Each event is captured as a raw message so the test can
// assert the matcher shape (direct handler list vs {matcher, hooks:[…]}).
type antigravityDoc map[string]map[string]json.RawMessage

func parseAntigravityDoc(t *testing.T, data []byte) antigravityDoc {
	t.Helper()
	var doc antigravityDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("failed to parse rendered Antigravity document: %v\n%s", err, data)
	}
	return doc
}

func TestAntigravityHooksHandler_DefaultInstallPath(t *testing.T) {
	t.Parallel()

	handler := NewAntigravityHooksHandler()
	got, err := handler.DefaultInstallPath("/tmp/project")
	if err != nil {
		t.Fatalf("DefaultInstallPath returned error: %v", err)
	}
	want := filepath.Join("/tmp/project", ".agents", "hooks.json")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("DefaultInstallPath mismatch (-want +got):\n%s", diff)
	}
}

func TestAntigravityHooksHandler_RenderDocument(t *testing.T) {
	t.Parallel()

	handler := NewAntigravityHooksHandler()
	data, err := handler.renderDocument("traceary")
	if err != nil {
		t.Fatalf("RenderDocument returned error: %v", err)
	}
	doc := parseAntigravityDoc(t, data)

	group, ok := doc["traceary"]
	if !ok {
		t.Fatalf("rendered document has no traceary group:\n%s", data)
	}

	wantEvents := []string{"PostToolUse", "PreInvocation", "PreToolUse", "Stop"}
	gotEvents := make([]string, 0, len(group))
	for event := range group {
		gotEvents = append(gotEvents, event)
	}
	if diff := cmp.Diff(wantEvents, sortedStrings(gotEvents)); diff != "" {
		t.Fatalf("traceary group events mismatch (-want +got):\n%s", diff)
	}

	t.Run("PreInvocation and Stop list handlers directly (matcher-less)", func(t *testing.T) {
		t.Parallel()

		for _, event := range []string{"PreInvocation", "Stop"} {
			var handlers []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
				Matcher string `json:"matcher"`
			}
			if err := json.Unmarshal(group[event], &handlers); err != nil {
				t.Fatalf("%s should be a direct handler list: %v\n%s", event, err, group[event])
			}
			if diff := cmp.Diff(1, len(handlers)); diff != "" {
				t.Fatalf("%s handler count mismatch (-want +got):\n%s", event, diff)
			}
			if handlers[0].Type != "command" {
				t.Fatalf("%s handler type = %q, want command", event, handlers[0].Type)
			}
			if handlers[0].Matcher != "" {
				t.Fatalf("%s handler must be matcher-less, got matcher %q", event, handlers[0].Matcher)
			}
			if handlers[0].Timeout != antigravityHookTimeoutSeconds {
				t.Fatalf("%s timeout = %d, want %d", event, handlers[0].Timeout, antigravityHookTimeoutSeconds)
			}
		}
	})

	t.Run("PreToolUse and PostToolUse wrap handlers in a run_command matcher", func(t *testing.T) {
		t.Parallel()

		for _, event := range []string{"PreToolUse", "PostToolUse"} {
			var entries []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			}
			if err := json.Unmarshal(group[event], &entries); err != nil {
				t.Fatalf("%s should be a {matcher, hooks} list: %v\n%s", event, err, group[event])
			}
			if diff := cmp.Diff(1, len(entries)); diff != "" {
				t.Fatalf("%s entry count mismatch (-want +got):\n%s", event, diff)
			}
			if diff := cmp.Diff("run_command", entries[0].Matcher); diff != "" {
				t.Fatalf("%s matcher mismatch (-want +got):\n%s", event, diff)
			}
			if diff := cmp.Diff(1, len(entries[0].Hooks)); diff != "" {
				t.Fatalf("%s nested hook count mismatch (-want +got):\n%s", event, diff)
			}
			if entries[0].Hooks[0].Type != "command" {
				t.Fatalf("%s nested hook type = %q, want command", event, entries[0].Hooks[0].Type)
			}
		}
	})

	t.Run("each event invokes the matching hidden runtime subcommand", func(t *testing.T) {
		t.Parallel()

		raw := string(data)
		for _, fragment := range []string{
			"'hook' 'antigravity' 'pre-invocation'",
			"'hook' 'antigravity' 'pre-tool-use'",
			"'hook' 'antigravity' 'post-tool-use'",
			"'hook' 'antigravity' 'stop'",
		} {
			if !strings.Contains(raw, fragment) {
				t.Fatalf("rendered document missing runtime command %q:\n%s", fragment, raw)
			}
		}
	})
}

func TestAntigravityHooksHandler_MergeDocument(t *testing.T) {
	t.Parallel()

	handler := NewAntigravityHooksHandler()

	t.Run("empty existing renders a fresh document with all events added", func(t *testing.T) {
		t.Parallel()

		encoded, diff, err := handler.mergeDocument(nil, "traceary")
		if err != nil {
			t.Fatalf("MergeDocument returned error: %v", err)
		}
		doc := parseAntigravityDoc(t, encoded)
		if _, ok := doc["traceary"]; !ok {
			t.Fatalf("merged document has no traceary group:\n%s", encoded)
		}
		want := []string{"PostToolUse", "PreInvocation", "PreToolUse", "Stop"}
		if cmpDiff := cmp.Diff(want, sortedStrings(diff.AddedEvents)); cmpDiff != "" {
			t.Fatalf("AddedEvents mismatch (-want +got):\n%s", cmpDiff)
		}
	})

	t.Run("preserves a foreign hook group and replaces the traceary group", func(t *testing.T) {
		t.Parallel()

		existing := []byte(`{
  "vendor-x": {
    "PreInvocation": [
      {"type": "command", "command": "vendor-x audit"}
    ]
  },
  "traceary": {
    "Stop": [
      {"type": "command", "command": "old-traceary stop"}
    ]
  }
}`)

		encoded, diff, err := handler.mergeDocument(existing, "traceary")
		if err != nil {
			t.Fatalf("MergeDocument returned error: %v", err)
		}
		doc := parseAntigravityDoc(t, encoded)

		// Foreign group preserved verbatim.
		vendor, ok := doc["vendor-x"]
		if !ok {
			t.Fatalf("vendor-x group was not preserved:\n%s", encoded)
		}
		if _, ok := vendor["PreInvocation"]; !ok {
			t.Fatalf("vendor-x PreInvocation was not preserved:\n%s", encoded)
		}
		if !strings.Contains(string(encoded), "vendor-x audit") {
			t.Fatalf("vendor-x command was not preserved verbatim:\n%s", encoded)
		}

		// Traceary group fully replaced with the four managed events.
		group, ok := doc["traceary"]
		if !ok {
			t.Fatalf("traceary group missing after merge:\n%s", encoded)
		}
		wantEvents := []string{"PostToolUse", "PreInvocation", "PreToolUse", "Stop"}
		gotEvents := make([]string, 0, len(group))
		for event := range group {
			gotEvents = append(gotEvents, event)
		}
		if cmpDiff := cmp.Diff(wantEvents, sortedStrings(gotEvents)); cmpDiff != "" {
			t.Fatalf("merged traceary events mismatch (-want +got):\n%s", cmpDiff)
		}
		if strings.Contains(string(encoded), "old-traceary stop") {
			t.Fatalf("stale traceary command survived the merge:\n%s", encoded)
		}

		// Stop existed before (refreshed); the other three are added.
		if cmpDiff := cmp.Diff([]string{"Stop"}, sortedStrings(diff.RefreshedEvents)); cmpDiff != "" {
			t.Fatalf("RefreshedEvents mismatch (-want +got):\n%s", cmpDiff)
		}
		if cmpDiff := cmp.Diff([]string{"PostToolUse", "PreInvocation", "PreToolUse"}, sortedStrings(diff.AddedEvents)); cmpDiff != "" {
			t.Fatalf("AddedEvents mismatch (-want +got):\n%s", cmpDiff)
		}
	})

	t.Run("re-merging an up-to-date document reports every event preserved", func(t *testing.T) {
		t.Parallel()

		first, _, err := handler.mergeDocument(nil, "traceary")
		if err != nil {
			t.Fatalf("initial MergeDocument returned error: %v", err)
		}
		_, diff, err := handler.mergeDocument(first, "traceary")
		if err != nil {
			t.Fatalf("re-merge returned error: %v", err)
		}
		want := []string{"PostToolUse", "PreInvocation", "PreToolUse", "Stop"}
		if cmpDiff := cmp.Diff(want, sortedStrings(diff.PreservedEvents)); cmpDiff != "" {
			t.Fatalf("PreservedEvents mismatch (-want +got):\n%s", cmpDiff)
		}
		if len(diff.AddedEvents)+len(diff.RefreshedEvents)+len(diff.RemovedEvents) != 0 {
			t.Fatalf("expected no add/refresh/remove on up-to-date re-merge, got %+v", diff)
		}
	})

	t.Run("non-object existing document is an error", func(t *testing.T) {
		t.Parallel()

		_, _, err := handler.mergeDocument([]byte(`["not", "an", "object"]`), "traceary")
		if err == nil {
			t.Fatalf("expected error for non-object existing document")
		}
	})
}

// sortedStrings returns a sorted copy of the given slice for stable assertions.
func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
