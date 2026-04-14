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
