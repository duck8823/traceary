package filesystem_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestHooksInspector_Inspect(t *testing.T) {
	t.Parallel()

	type want struct {
		hasHooksField          bool
		hasTracearyManagedHook bool
		sentinel               error
	}

	tests := []struct {
		name    string
		payload string
		want    want
	}{
		{
			name: "detects traceary managed hook by name prefix",
			payload: `{
              "hooks": {
                "SessionStart": [
                  {
                    "matcher": "*",
                    "hooks": [
                      {
                        "name": "traceary-session-start",
                        "type": "command",
                        "command": "bash /tmp/traceary-session.sh claude start"
                      }
                    ]
                  }
                ]
              }
            }`,
			want: want{hasHooksField: true, hasTracearyManagedHook: true},
		},
		{
			name: "detects traceary managed hook by direct runtime command",
			payload: `{
              "hooks": {
                "SessionEnd": [
                  {
                    "matcher": "*",
                    "hooks": [
                      {
                        "type": "command",
                        "command": "'traceary' 'hook' 'session' 'codex' 'end'"
                      }
                    ]
                  }
                ]
              }
            }`,
			want: want{hasHooksField: true, hasTracearyManagedHook: true},
		},
		{
			name: "detects traceary managed hook by named custom-wrapper direct runtime command",
			payload: `{
              "hooks": {
                "SessionStart": [
                  {
                    "matcher": "*",
                    "hooks": [
                      {
                        "name": "traceary-session-start",
                        "type": "command",
                        "command": "'/tmp/custom-traceary-wrapper' 'hook' 'session' 'claude' 'start'"
                      }
                    ]
                  }
                ]
              }
            }`,
			want: want{hasHooksField: true, hasTracearyManagedHook: true},
		},
		{
			name: "hooks field present but no traceary hook",
			payload: `{
              "hooks": {
                "SessionStart": [
                  {
                    "matcher": "*",
                    "hooks": [
                      {
                        "type": "command",
                        "command": "echo unrelated"
                      }
                    ]
                  }
                ]
              }
            }`,
			want: want{hasHooksField: true, hasTracearyManagedHook: false},
		},
		{
			name:    "valid JSON object without hooks field",
			payload: `{"theme": "dark"}`,
			want:    want{hasHooksField: false, hasTracearyManagedHook: false},
		},
		{
			name:    "non-object payload",
			payload: `["hooks"]`,
			want:    want{sentinel: application.ErrHookConfigNotJSONObject},
		},
		{
			name:    "hooks field with wrong shape",
			payload: `{"hooks": "not-an-object"}`,
			want:    want{hasHooksField: true, sentinel: application.ErrHookConfigInvalidHooksField},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inspector := filesystem.NewHooksInspector()
			gotHasHooks, gotHasTraceary, err := inspector.Inspect([]byte(tt.payload))

			if tt.want.sentinel != nil {
				if err == nil {
					t.Fatalf("Inspect() error = nil, want %v", tt.want.sentinel)
				}
				if !errors.Is(err, tt.want.sentinel) {
					t.Fatalf("Inspect() error = %v, want %v", err, tt.want.sentinel)
				}
			} else if err != nil {
				t.Fatalf("Inspect() error = %v, want nil", err)
			}

			if diff := cmp.Diff(tt.want.hasHooksField, gotHasHooks); diff != "" {
				t.Errorf("hasHooksField mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.want.hasTracearyManagedHook, gotHasTraceary); diff != "" {
				t.Errorf("hasTracearyManagedHook mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHooksInspector_DuplicateManagedHooks(t *testing.T) {
	t.Parallel()

	inspector := filesystem.NewHooksInspector()
	duplicates, err := inspector.DuplicateManagedHooks([]byte(`{
      "hooks": {
        "PostToolUse": [
          {
            "matcher": "",
            "hooks": [
              {"name": "traceary-audit", "type": "command", "command": "'traceary' 'hook' 'audit' 'codex'"},
              {"name": "traceary-audit", "type": "command", "command": "'traceary' 'hook' 'audit' 'codex'"},
              {"name": "user-audit", "type": "command", "command": "echo user"}
            ]
          }
        ],
        "SessionStart": [
          {
            "hooks": [
              {"name": "traceary-session-start", "type": "command", "command": "'traceary' 'hook' 'session' 'codex' 'start'"}
            ]
          }
        ]
      }
    }`))
	if err != nil {
		t.Fatalf("DuplicateManagedHooks() error = %v", err)
	}

	want := []application.HookDuplicate{{
		Event:      "PostToolUse",
		Matcher:    "",
		ManagedKey: "traceary-audit.sh:codex",
		Count:      2,
	}}
	if diff := cmp.Diff(want, duplicates); diff != "" {
		t.Fatalf("duplicates mismatch (-want +got):\n%s", diff)
	}
}

func TestHooksInspector_DuplicateManagedHooks_AllowsDistinctMatchers(t *testing.T) {
	t.Parallel()

	inspector := filesystem.NewHooksInspector()
	duplicates, err := inspector.DuplicateManagedHooks([]byte(`{
      "hooks": {
        "PostToolUse": [
          {
            "matcher": "Bash",
            "hooks": [{"type": "command", "command": "'traceary' 'hook' 'audit' 'claude'"}]
          },
          {
            "matcher": "mcp__.*",
            "hooks": [{"type": "command", "command": "'traceary' 'hook' 'audit' 'claude'"}]
          }
        ]
      }
    }`))
	if err != nil {
		t.Fatalf("DuplicateManagedHooks() error = %v", err)
	}
	if len(duplicates) != 0 {
		t.Fatalf("duplicates = %+v, want none for distinct matchers", duplicates)
	}
}

func TestHooksInspector_ManagedCoverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		want    application.HookManagedCoverage
	}{
		{
			name: "reports complete Gemini enrichment coverage",
			payload: `{
			  "hooks": {
			    "SessionStart": [{"hooks": [{"name": "traceary-session-start", "type": "command", "command": "'traceary' 'hook' 'session' 'gemini' 'start'"}]}],
			    "BeforeAgent": [{"hooks": [{"name": "traceary-prompt", "type": "command", "command": "'traceary' 'hook' 'prompt' 'gemini'"}]}],
			    "AfterAgent": [{"hooks": [{"name": "traceary-transcript", "type": "command", "command": "'traceary' 'hook' 'transcript' 'gemini'"}]}],
			    "AfterTool": [{"matcher": "run_shell_command", "hooks": [{"name": "traceary-audit", "type": "command", "command": "'traceary' 'hook' 'audit' 'gemini'"}]}],
			    "PreCompress": [{"hooks": [{"name": "traceary-pre-compress", "type": "command", "command": "'traceary' 'hook' 'compact' 'gemini' 'pre-compact'"}]}]
			  }
			}`,
			want: application.HookManagedCoverage{
				HasPrompt:     true,
				HasTranscript: true,
				HasAudit:      true,
				HasCompact:    true,
			},
		},
		{
			name: "reports legacy boundary and audit only config as missing prompt and transcript",
			payload: `{
			  "hooks": {
			    "SessionStart": [{"hooks": [{"name": "traceary-session-start", "type": "command", "command": "'traceary' 'hook' 'session' 'gemini' 'start'"}]}],
			    "SessionEnd": [{"hooks": [{"name": "traceary-session-end", "type": "command", "command": "'traceary' 'hook' 'session' 'gemini' 'end'"}]}],
			    "AfterTool": [{"matcher": "run_shell_command", "hooks": [{"name": "traceary-audit", "type": "command", "command": "'traceary' 'hook' 'audit' 'gemini'"}]}]
			  }
			}`,
			want: application.HookManagedCoverage{
				HasAudit: true,
			},
		},
		{
			name: "recognizes non-canonical traceary binary through entry names",
			payload: `{
			  "hooks": {
			    "BeforeAgent": [{"hooks": [{"name": "traceary-prompt", "type": "command", "command": "'/tmp/traceary-qa' 'hook' 'prompt' 'gemini'"}]}],
			    "AfterAgent": [{"hooks": [{"name": "traceary-transcript", "type": "command", "command": "'/tmp/traceary-qa' 'hook' 'transcript' 'gemini'"}]}],
			    "AfterTool": [{"hooks": [{"name": "traceary-audit", "type": "command", "command": "'/tmp/traceary-qa' 'hook' 'audit' 'gemini'"}]}]
			  }
			}`,
			want: application.HookManagedCoverage{
				HasPrompt:     true,
				HasTranscript: true,
				HasAudit:      true,
			},
		},
		{
			name: "recognizes legacy script-form transcript hooks",
			payload: `{
			  "hooks": {
			    "BeforeAgent": [{"hooks": [{"type": "command", "command": "bash '/scripts/traceary-prompt.sh' 'gemini'"}]}],
			    "AfterAgent": [{"hooks": [{"type": "command", "command": "bash '/scripts/traceary-transcript.sh' 'gemini'"}]}],
			    "AfterTool": [{"hooks": [{"type": "command", "command": "bash '/scripts/traceary-audit.sh' 'gemini'"}]}]
			  }
			}`,
			want: application.HookManagedCoverage{
				HasPrompt:     true,
				HasTranscript: true,
				HasAudit:      true,
			},
		},
		{
			name:    "missing hooks field has zero coverage",
			payload: `{"theme":"dark"}`,
			want:    application.HookManagedCoverage{},
		},
		{
			name: "ignores user managed commands",
			payload: `{
			  "hooks": {
			    "BeforeAgent": [{"hooks": [{"name": "user-prompt", "type": "command", "command": "'/tmp/custom' 'hook' 'prompt' 'gemini'"}]}]
			  }
			}`,
			want: application.HookManagedCoverage{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inspector := filesystem.NewHooksInspector()
			got, err := inspector.ManagedCoverage([]byte(tt.payload))
			if err != nil {
				t.Fatalf("ManagedCoverage() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("coverage mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHookManagedCoverage_MissingEnrichment(t *testing.T) {
	t.Parallel()

	got := (application.HookManagedCoverage{HasAudit: true}).MissingEnrichment()
	want := []string{"prompt", "transcript"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("MissingEnrichment() mismatch (-want +got):\n%s", diff)
	}
}
