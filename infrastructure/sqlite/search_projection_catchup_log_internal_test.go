package sqlite

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestLogSearchProjectionCatchUp_LogsSkipsButNotQuietStates(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	tests := []struct {
		name      string
		result    apptypes.SearchProjectionCatchUpResult
		wantLog   bool
		wantLevel string
		wantSub   []string
	}{
		{
			name: "budget mismatch skip is visible and actionable",
			result: apptypes.SearchProjectionCatchUpResult{
				Action:        "skipped",
				State:         "rebuilding",
				Phase:         "source",
				GenerationID:  "gen-stuck",
				SkippedReason: "budget does not match generation configuration",
			},
			wantLog:   true,
			wantLevel: "WARN",
			wantSub: []string{
				"search projection catch-up skipped",
				"budget does not match generation configuration",
				"will not advance on its own",
				"gen-stuck",
			},
		},
		{
			name: "parked deterministic failure names the class",
			result: apptypes.SearchProjectionCatchUpResult{
				Action:        "skipped",
				State:         "failed",
				Phase:         "complete",
				GenerationID:  "gen-oversize",
				SkippedReason: "parked after generation failure decoded_bytes; resume or abort explicitly to unblock automatic progress",
			},
			wantLog:   true,
			wantLevel: "WARN",
			wantSub: []string{
				"search projection catch-up skipped",
				"parked after generation failure decoded_bytes",
				"gen-oversize",
			},
		},
		{
			name:    "already_complete stays quiet",
			result:  apptypes.SearchProjectionCatchUpResult{Action: "already_complete", Completed: true},
			wantLog: false,
		},
		{
			name:    "none stays quiet",
			result:  apptypes.SearchProjectionCatchUpResult{Action: "none"},
			wantLog: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			logSearchProjectionCatchUp(tt.result, nil)
			got := buf.String()
			if tt.wantLog {
				if got == "" {
					t.Fatal("expected a log line, got none")
				}
				if !strings.Contains(got, "level="+tt.wantLevel) && !strings.Contains(got, "level="+strings.ToUpper(tt.wantLevel)) {
					// slog text handler prints level=WARN
					if !strings.Contains(got, tt.wantLevel) {
						t.Fatalf("log missing level %q: %s", tt.wantLevel, got)
					}
				}
				for _, sub := range tt.wantSub {
					if !strings.Contains(got, sub) {
						t.Fatalf("log missing %q: %s", sub, got)
					}
				}
				return
			}
			if diff := cmp.Diff("", got); diff != "" {
				t.Fatalf("quiet state logged unexpectedly (-want +got):\n%s\nlog=%q", diff, got)
			}
		})
	}
}
