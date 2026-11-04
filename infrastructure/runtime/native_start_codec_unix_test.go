//go:build darwin || linux

package runtime

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	domain "github.com/duck8823/traceary/domain/runtime"
)

// Private native metadata representation tests do not authenticate a process or
// qualify a native version. Actual owner/source checks belong to the adapter.
func nativeStartFixture() (nativeStartPlan, map[string]string, []byte) {
	plan := nativeStartPlan{Identity: domain.LaunchIdentity{Run: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32), Epoch: strings.Repeat("3", 32)}, SessionID: "12345678-1234-4234-8234-123456789abc", CWD: "/project", Store: "/store.db", Namespace: "/native/projects", Source: "resume"}
	env := map[string]string{"TRACEARY_RUNTIME_RUN_ID": plan.Identity.Run, "TRACEARY_RUNTIME_GENERATION": plan.Identity.Generation, "TRACEARY_RUNTIME_EPOCH": plan.Identity.Epoch, "TRACEARY_DB_PATH": plan.Store}
	data := []byte(`{"hook_event_name":"SessionStart","session_id":"12345678-1234-4234-8234-123456789abc","cwd":"/project","source":"resume","transcript_path":"/native/projects/-project/12345678-1234-4234-8234-123456789abc.jsonl","scratchpad_dir":"/native/scratch","context_tokens":12}`)
	return plan, env, data
}
func TestNativeStartCodecMatchesOnlyItsPlannedNativeIdentityAndHints(t *testing.T) {
	plan, env, data := nativeStartFixture()
	event, err := decodeNativeStart(data, plan, env)
	if err != nil || event.Plan != plan || event.TranscriptPath != "/native/projects/-project/"+plan.SessionID+".jsonl" {
		t.Fatalf("qualified representation lost: %+v %v", event, err)
	}
	for _, key := range []string{"TRACEARY_RUNTIME_RUN_ID", "TRACEARY_RUNTIME_GENERATION", "TRACEARY_RUNTIME_EPOCH", "TRACEARY_DB_PATH"} {
		changed := make(map[string]string, len(env))
		for k, v := range env {
			changed[k] = v
		}
		changed[key] = "other"
		if _, err := decodeNativeStart(data, plan, changed); !errors.Is(err, domain.ErrChanged) {
			t.Fatalf("%s mismatch accepted: %v", key, err)
		}
	}
}
func TestNativeStartCodecRefusesAmbiguousPayloadAndNamespaceEscape(t *testing.T) {
	plan, env, data := nativeStartFixture()
	for _, tc := range []struct{ name, old, new string }{
		{"foreign native session", "12345678-1234-4234-8234-123456789abc", "12345678-1234-4234-8234-123456789def"},
		{"foreign cwd", `"cwd":"/project"`, `"cwd":"/other"`},
		{"wrong event", "SessionStart", "UserPromptSubmit"},
		{"wrong source", `"source":"resume"`, `"source":"startup"`},
		{"namespace sibling", "/native/projects/-project/", "/native/projects-other/-project/"},
		{"namespace traversal", "/native/projects/-project/", "/native/projects/../-project/"},
		{"different history basename", ".jsonl", "-other.jsonl"},
		{"missing field", `"source":"resume",`, ``},
		{"null", `"source":"resume"`, `"source":null`},
		{"wrong type", `"cwd":"/project"`, `"cwd":true`},
		{"duplicate", `"source":"resume"`, `"source":"resume","source":"resume"`},
		{"case alias", `"session_id"`, `"Session_ID"`},
		{"camel alias", `"session_id"`, `"sessionId"`},
		{"unicode case alias", `"source"`, `"ſource"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeNativeStart([]byte(strings.Replace(string(data), tc.old, tc.new, 1)), plan, env); err == nil {
				t.Fatal("invalid native event accepted")
			}
		})
	}
	for _, bad := range [][]byte{append(append([]byte{}, data...), []byte(` {}`)...), append(append([]byte{}, data...), 255), []byte(`[]`), []byte(`null`)} {
		if _, err := decodeNativeStart(bad, plan, env); err == nil {
			t.Fatal("malformed envelope accepted")
		}
	}
	data = append(data[:len(data)-1], []byte(`,"prompt":"synthetic-private-body","api_key":"synthetic-not-a-credential"}`)...)
	event, err := decodeNativeStart(data, plan, env)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "synthetic-private") || strings.Contains(string(encoded), "api_key") {
		t.Fatal("borrowed vendor fields retained in reduced event")
	}
}
func FuzzNativeStartCodecDoesNotPromoteUnplannedMetadata(f *testing.F) {
	_, _, data := nativeStartFixture()
	f.Add(data)
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		plan, env, _ := nativeStartFixture()
		event, err := decodeNativeStart(data, plan, env)
		if err == nil && (event.Plan != plan || event.TranscriptPath == "") {
			t.Fatal("unplanned native metadata promoted")
		}
	})
}

func TestNativeStartCodecBoundsAndInvalidPlansNeverQualify(t *testing.T) {
	plan, env, data := nativeStartFixture()
	if _, err := decodeNativeStart(make([]byte, maxNativeStartBytes+1), plan, env); !errors.Is(err, domain.ErrLimit) {
		t.Fatalf("oversize accepted: %v", err)
	}
	for _, kind := range []string{"zero identity", "noncanonical uuid", "source", "relative cwd", "unclean namespace", "control store"} {
		t.Run(kind, func(t *testing.T) {
			bad := plan
			switch kind {
			case "zero identity":
				bad.Identity = domain.LaunchIdentity{}
			case "noncanonical uuid":
				bad.SessionID = strings.ToUpper(bad.SessionID)
			case "source":
				bad.Source = "compact"
			case "relative cwd":
				bad.CWD = "project"
			case "unclean namespace":
				bad.Namespace = "/native/../projects"
			case "control store":
				bad.Store = "/store\n.db"
			}
			if _, err := decodeNativeStart(data, bad, env); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("invalid plan accepted: %v", err)
			}
		})
	}
}
