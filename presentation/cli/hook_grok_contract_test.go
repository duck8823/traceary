package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type grokHostContract struct {
	Hosts struct {
		Grok struct {
			Events map[string]grokContractEvent `json:"events"`
		} `json:"grok"`
	} `json:"hosts"`
}

type grokContractEvent struct {
	Observed        bool     `json:"observed"`
	TracearySupport string   `json:"traceary_support"`
	Fixture         *string  `json:"fixture"`
	VariantFixtures []string `json:"variant_fixtures"`
	Note            string   `json:"note"`
}

func TestGrokHostContract(t *testing.T) {
	t.Parallel()

	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	contractPath := filepath.Join(repositoryRoot, "docs", "hooks", "host-contract.json")
	contractBytes, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatalf("read Grok host contract: %v", err)
	}

	var contract grokHostContract
	if err := json.Unmarshal(contractBytes, &contract); err != nil {
		t.Fatalf("decode Grok host contract: %v", err)
	}

	expectedHookNames := map[string]string{
		"SessionStart":     "session_start",
		"UserPromptSubmit": "user_prompt_submit",
		"PreToolUse":       "pre_tool_use",
		"PostToolUse":      "post_tool_use",
		"Stop":             "stop",
		"PreCompact":       "pre_compact",
		"PostCompact":      "post_compact",
	}
	referencedFixtures := make(map[string]struct{})

	for eventName, event := range contract.Hosts.Grok.Events {
		t.Run(eventName, func(t *testing.T) {
			switch event.TracearySupport {
			case "supported", "best_effort":
				if !event.Observed {
					t.Fatal("supported Grok event must be live-observed")
				}
				if event.Fixture == nil {
					t.Fatal("supported Grok event must reference a fixture")
				}
				expectedHookName, ok := expectedHookNames[eventName]
				if !ok {
					t.Fatal("supported Grok event has no expected hook name")
				}
				referencedFixtures[*event.Fixture] = struct{}{}
				validateGrokFixture(t, repositoryRoot, *event.Fixture, expectedHookName)
				for _, fixture := range event.VariantFixtures {
					referencedFixtures[fixture] = struct{}{}
					validateGrokFixture(t, repositoryRoot, fixture, expectedHookName)
				}
			case "unavailable":
				if event.Observed {
					t.Fatal("unavailable Grok event must not be marked live-observed")
				}
				if event.Fixture != nil {
					t.Fatal("unavailable Grok event must not reference a fixture")
				}
				if strings.TrimSpace(event.Note) == "" {
					t.Fatal("unavailable Grok event must explain the limitation")
				}
			default:
				t.Fatalf("unknown Traceary support level %q", event.TracearySupport)
			}
		})
	}

	fixtureDirectory := filepath.Join(repositoryRoot, "presentation", "cli", "testdata", "grok_hooks", "v0.2.99")
	fixtures, err := filepath.Glob(filepath.Join(fixtureDirectory, "*.json"))
	if err != nil {
		t.Fatalf("list Grok fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no Grok fixtures found")
	}
	for _, fixture := range fixtures {
		repositoryRelativeFixture, err := filepath.Rel(repositoryRoot, fixture)
		if err != nil {
			t.Fatalf("resolve repository-relative Grok fixture path: %v", err)
		}
		repositoryRelativeFixture = filepath.ToSlash(repositoryRelativeFixture)
		if _, ok := referencedFixtures[repositoryRelativeFixture]; !ok {
			t.Errorf("Grok fixture %s is not referenced by the host contract", repositoryRelativeFixture)
		}
		fixtureBytes, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatalf("read Grok fixture %s: %v", fixture, err)
		}
		for _, privateValue := range []string{"/Users/", "/private/tmp/", "duck8823"} {
			if strings.Contains(string(fixtureBytes), privateValue) {
				t.Errorf("Grok fixture %s contains private value %q", fixture, privateValue)
			}
		}
	}
}

func validateGrokFixture(t *testing.T, repositoryRoot, fixturePath, expectedHookName string) {
	t.Helper()

	expectedDirectory := filepath.Join("presentation", "cli", "testdata", "grok_hooks", "v0.2.99")
	cleanFixturePath := filepath.Clean(filepath.FromSlash(fixturePath))
	if filepath.Dir(cleanFixturePath) != expectedDirectory {
		t.Fatalf("Grok fixture %s must be directly under %s", fixturePath, filepath.ToSlash(expectedDirectory))
	}

	fixtureBytes, err := os.ReadFile(filepath.Join(repositoryRoot, cleanFixturePath))
	if err != nil {
		t.Fatalf("read Grok fixture %s: %v", fixturePath, err)
	}

	var payload map[string]any
	if err := json.Unmarshal(fixtureBytes, &payload); err != nil {
		t.Fatalf("decode Grok fixture %s: %v", fixturePath, err)
	}

	requireStringField(t, payload, "hookEventName")
	if payload["hookEventName"] != expectedHookName {
		t.Fatalf("hookEventName = %v, want %q", payload["hookEventName"], expectedHookName)
	}
	for _, field := range []string{"sessionId", "cwd", "workspaceRoot", "timestamp"} {
		requireStringField(t, payload, field)
	}

	switch expectedHookName {
	case "session_start":
		requireStringField(t, payload, "source")
		if _, exists := payload["transcriptPath"]; exists {
			t.Error("SessionStart fixture must preserve observed absence of transcriptPath")
		}
	case "user_prompt_submit":
		for _, field := range []string{"prompt", "promptId", "transcriptPath"} {
			requireStringField(t, payload, field)
		}
	case "pre_tool_use":
		validateGrokToolFields(t, payload)
		requireStringField(t, payload, "permissionMode")
	case "post_tool_use":
		validateGrokToolFields(t, payload)
		requireObjectField(t, payload, "toolResult")
		requireBoolField(t, payload, "toolResultTruncated")
		requireBoolField(t, payload, "isBackgrounded")
	case "stop":
		for _, field := range []string{"promptId", "reason", "transcriptPath"} {
			requireStringField(t, payload, field)
		}
	case "pre_compact", "post_compact":
		for _, field := range []string{"source", "transcriptPath"} {
			requireStringField(t, payload, field)
		}
		if _, exists := payload["summary"]; exists {
			t.Error("compact fixture must preserve observed absence of summary body")
		}
	default:
		t.Fatalf("unsupported fixture hookEventName %q", expectedHookName)
	}
}

func validateGrokToolFields(t *testing.T, payload map[string]any) {
	t.Helper()
	for _, field := range []string{"toolName", "toolUseId", "transcriptPath"} {
		requireStringField(t, payload, field)
	}
	requireObjectField(t, payload, "toolInput")
	requireBoolField(t, payload, "toolInputTruncated")
}

func requireStringField(t *testing.T, payload map[string]any, field string) {
	t.Helper()
	value, ok := payload[field].(string)
	if !ok || value == "" {
		t.Errorf("%s must be a non-empty string, got %s", field, describeValue(payload[field]))
	}
}

func requireBoolField(t *testing.T, payload map[string]any, field string) {
	t.Helper()
	if _, ok := payload[field].(bool); !ok {
		t.Errorf("%s must be a boolean, got %s", field, describeValue(payload[field]))
	}
}

func requireObjectField(t *testing.T, payload map[string]any, field string) {
	t.Helper()
	if _, ok := payload[field].(map[string]any); !ok {
		t.Errorf("%s must be an object, got %s", field, describeValue(payload[field]))
	}
}

func describeValue(value any) string {
	return fmt.Sprintf("%T(%v)", value, value)
}
