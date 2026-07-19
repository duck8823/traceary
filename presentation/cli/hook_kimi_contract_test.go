package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type kimiHostContract struct {
	Hosts struct {
		Kimi struct {
			Events map[string]kimiContractEvent `json:"events"`
		} `json:"kimi"`
	} `json:"hosts"`
}

type kimiContractEvent struct {
	Observed        bool     `json:"observed"`
	TracearySupport string   `json:"traceary_support"`
	Fixture         *string  `json:"fixture"`
	VariantFixtures []string `json:"variant_fixtures"`
	Note            string   `json:"note"`
}

func TestKimiHostContract(t *testing.T) {
	t.Parallel()

	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	contractPath := filepath.Join(repositoryRoot, "docs", "hooks", "host-contract.json")
	contractBytes, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatalf("read Kimi host contract: %v", err)
	}

	var contract kimiHostContract
	if err := json.Unmarshal(contractBytes, &contract); err != nil {
		t.Fatalf("decode Kimi host contract: %v", err)
	}

	expectedHookNames := map[string]string{
		"SessionStart":       "SessionStart",
		"UserPromptSubmit":   "UserPromptSubmit",
		"PreToolUse":         "PreToolUse",
		"PostToolUse":        "PostToolUse",
		"PostToolUseFailure": "PostToolUseFailure",
		"Stop":               "Stop",
		"SessionEnd":         "SessionEnd",
		"SubagentStart":      "SubagentStart",
		"SubagentStop":       "SubagentStop",
		"Notification":       "Notification",
	}
	referencedFixtures := make(map[string]struct{})

	for eventName, event := range contract.Hosts.Kimi.Events {
		t.Run(eventName, func(t *testing.T) {
			switch event.TracearySupport {
			case "supported", "best_effort":
				if !event.Observed {
					t.Fatal("supported Kimi event must be live-observed")
				}
				if event.Fixture == nil {
					t.Fatal("supported Kimi event must reference a fixture")
				}
				expectedHookName, ok := expectedHookNames[eventName]
				if !ok {
					t.Fatal("supported Kimi event has no expected hook name")
				}
				referencedFixtures[*event.Fixture] = struct{}{}
				validateKimiFixture(t, repositoryRoot, *event.Fixture, expectedHookName)
				for _, fixture := range event.VariantFixtures {
					referencedFixtures[fixture] = struct{}{}
					validateKimiFixture(t, repositoryRoot, fixture, expectedHookName)
				}
			case "unavailable":
				if event.Observed && event.Fixture == nil {
					t.Fatal("observed but unavailable Kimi event must still reference a fixture")
				}
				if !event.Observed && event.Fixture != nil {
					t.Fatal("unobserved Kimi event must not reference a fixture")
				}
				if strings.TrimSpace(event.Note) == "" {
					t.Fatal("unavailable Kimi event must explain the limitation")
				}
				if event.Fixture != nil {
					expectedHookName, ok := expectedHookNames[eventName]
					if !ok {
						t.Fatal("observed Kimi event has no expected hook name")
					}
					referencedFixtures[*event.Fixture] = struct{}{}
					validateKimiFixture(t, repositoryRoot, *event.Fixture, expectedHookName)
				}
			default:
				t.Fatalf("unknown Traceary support level %q", event.TracearySupport)
			}
		})
	}

	fixtureDirectory := filepath.Join(repositoryRoot, "presentation", "cli", "testdata", "kimi_hooks", "v0.27.0")
	fixtures, err := filepath.Glob(filepath.Join(fixtureDirectory, "*.json"))
	if err != nil {
		t.Fatalf("list Kimi fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no Kimi fixtures found")
	}
	for _, fixture := range fixtures {
		repositoryRelativeFixture, err := filepath.Rel(repositoryRoot, fixture)
		if err != nil {
			t.Fatalf("resolve repository-relative Kimi fixture path: %v", err)
		}
		repositoryRelativeFixture = filepath.ToSlash(repositoryRelativeFixture)
		if _, ok := referencedFixtures[repositoryRelativeFixture]; !ok {
			t.Errorf("Kimi fixture %s is not referenced by the host contract", repositoryRelativeFixture)
		}
		fixtureBytes, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatalf("read Kimi fixture %s: %v", fixture, err)
		}
		for _, privateValue := range []string{"/Users/", "/private/tmp/", "duck8823"} {
			if strings.Contains(string(fixtureBytes), privateValue) {
				t.Errorf("Kimi fixture %s contains private value %q", fixture, privateValue)
			}
		}
	}
}

func validateKimiFixture(t *testing.T, repositoryRoot, fixturePath, expectedHookName string) {
	t.Helper()

	expectedDirectory := filepath.Join("presentation", "cli", "testdata", "kimi_hooks", "v0.27.0")
	cleanFixturePath := filepath.Clean(filepath.FromSlash(fixturePath))
	if filepath.Dir(cleanFixturePath) != expectedDirectory {
		t.Fatalf("Kimi fixture %s must be directly under %s", fixturePath, filepath.ToSlash(expectedDirectory))
	}

	fixtureBytes, err := os.ReadFile(filepath.Join(repositoryRoot, cleanFixturePath))
	if err != nil {
		t.Fatalf("read Kimi fixture %s: %v", fixturePath, err)
	}

	var payload map[string]any
	if err := json.Unmarshal(fixtureBytes, &payload); err != nil {
		t.Fatalf("decode Kimi fixture %s: %v", fixturePath, err)
	}

	requireStringField(t, payload, "hook_event_name")
	if payload["hook_event_name"] != expectedHookName {
		t.Fatalf("hook_event_name = %v, want %q", payload["hook_event_name"], expectedHookName)
	}
	for _, field := range []string{"session_id", "cwd"} {
		requireStringField(t, payload, field)
	}

	switch expectedHookName {
	case "SessionStart":
		requireStringField(t, payload, "source")
		if _, exists := payload["model"]; exists {
			t.Error("SessionStart fixture must preserve observed absence of model")
		}
	case "UserPromptSubmit":
		blocks, ok := payload["prompt"].([]any)
		if !ok || len(blocks) == 0 {
			t.Fatalf("prompt must be a non-empty content-block array, got %s", describeValue(payload["prompt"]))
		}
		block, ok := blocks[0].(map[string]any)
		if !ok {
			t.Fatalf("prompt block must be an object, got %s", describeValue(blocks[0]))
		}
		requireStringField(t, block, "type")
		requireStringField(t, block, "text")
	case "PreToolUse", "PostToolUse", "PostToolUseFailure":
		requireStringField(t, payload, "tool_name")
		requireObjectField(t, payload, "tool_input")
		requireStringField(t, payload, "tool_call_id")
		if expectedHookName == "PostToolUse" {
			requireStringField(t, payload, "tool_output")
		}
		if expectedHookName == "PostToolUseFailure" {
			requireObjectField(t, payload, "error")
			errorValue, _ := payload["error"].(map[string]any)
			requireStringField(t, errorValue, "message")
		}
	case "Stop":
		requireBoolField(t, payload, "stop_hook_active")
		if _, exists := payload["transcript_path"]; exists {
			t.Error("Stop fixture must preserve observed absence of transcript_path")
		}
	case "SessionEnd":
		requireStringField(t, payload, "reason")
	case "SubagentStart":
		requireStringField(t, payload, "agent_name")
		requireStringField(t, payload, "prompt")
	case "SubagentStop":
		requireStringField(t, payload, "agent_name")
		requireStringField(t, payload, "response")
	case "Notification":
		requireStringField(t, payload, "notification_type")
		requireStringField(t, payload, "source_kind")
	default:
		t.Fatalf("unsupported fixture hook_event_name %q", expectedHookName)
	}
}
