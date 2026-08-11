package sqlite

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

type catchUpLogHandler struct {
	records []slog.Record
}

func (h *catchUpLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *catchUpLogHandler) Handle(_ context.Context, record slog.Record) error {
	h.records = append(h.records, record)
	return nil
}
func (h *catchUpLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *catchUpLogHandler) WithGroup(string) slog.Handler      { return h }

func TestLogSearchProjectionCatchUp_SingleRowLockCapSeparatesFromTheTransientMessage(t *testing.T) {
	tests := []struct {
		name           string
		phase          string
		wantCheckpoint any
	}{
		{
			// The source walk stops at the checkpoint, so it identifies the row.
			name:           "the source checkpoint identifies where the walk stopped",
			phase:          "source",
			wantCheckpoint: int64(1842),
		},
		{
			// Inventory advances a cursor and never moves the checkpoint. The
			// number is real but describes other work, and a stop position that
			// is exactly wrong is worse than one that is absent.
			name:           "no checkpoint is reported for work that does not use it",
			phase:          "inventory",
			wantCheckpoint: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := &catchUpLogHandler{}
			previous := slog.Default()
			slog.SetDefault(slog.New(handler))
			t.Cleanup(func() { slog.SetDefault(previous) })

			result := apptypes.SearchProjectionCatchUpResult{
				Action:       "resume",
				State:        "rebuilding",
				Phase:        test.phase,
				Checkpoint:   1842,
				GenerationID: "gen-stuck",
			}
			err := xerrors.Errorf("wrapped: %w", &apptypes.SearchProjectionNoProgressError{
				Code:   apptypes.SearchProjectionNoProgressSingleRowLockDurationCap,
				Reason: "single row exceeded lock cap",
			})

			logSearchProjectionCatchUp(result, err)

			if diff := cmp.Diff(1, len(handler.records)); diff != "" {
				t.Fatalf("unexpected record count (-want +got):\n%s", diff)
			}
			record := handler.records[0]
			if diff := cmp.Diff("search projection catch-up committed no row at the minimum batch size; search stays incomplete until it advances", record.Message); diff != "" {
				t.Fatalf("unexpected log message (-want +got):\n%s", diff)
			}
			attrs := map[string]any{}
			record.Attrs(func(attr slog.Attr) bool {
				attrs[attr.Key] = attr.Value.Any()
				return true
			})
			if diff := cmp.Diff(test.wantCheckpoint, attrs["checkpoint"]); diff != "" {
				t.Fatalf("unexpected checkpoint attribute (-want +got):\n%s", diff)
			}
			// Resume, not abort: LockTime is outside the generation's config
			// hash, so a larger cap is accepted without discarding progress.
			recovery, _ := attrs["recovery"].(string)
			if !strings.Contains(recovery, "resume --until-complete --lock-time") {
				t.Fatalf("recovery attribute = %q, want a resume command with a larger lock time", recovery)
			}
			if strings.Contains(record.Message, "abort") || strings.Contains(recovery, "abort") {
				t.Fatalf("message %q / recovery %q must not send an operator to abort a generation", record.Message, recovery)
			}
		})
	}
}

func TestLogSearchProjectionCatchUp_LogsSkipsButNotQuietStates(t *testing.T) {
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
