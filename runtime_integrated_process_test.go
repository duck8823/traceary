//go:build darwin || linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// Owns only a read-only CLI attach client, never the daemon or PTY child.
// This is a synthetic process oracle, not installed-AI qualification.
type integratedCLIOutput struct {
	command   *exec.Cmd
	decoder   *json.Decoder
	stdout    io.Reader
	selection map[string]string
	waited    bool
	cursor    uint64
}

type integratedCLIFrame struct {
	Kind string `json:"kind"`
	Info struct {
		Selection map[string]string `json:"selection"`
		Ended     bool              `json:"ended"`
		Base      string            `json:"base"`
		Head      string            `json:"head"`
	} `json:"info"`
	Start string `json:"start"`
	End   string `json:"end"`
	Data  []byte `json:"data"`
	Gap   struct {
		Reason          string `json:"reason"`
		From            string `json:"from"`
		To              string `json:"to"`
		DeliveryUnknown *bool  `json:"delivery_unknown"`
	} `json:"gap"`
}

func startIntegratedCLIOutput(t *testing.T, cmd *exec.Cmd, selection map[string]string) *integratedCLIOutput {
	t.Helper()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &integratedCLIOutput{command: cmd, decoder: json.NewDecoder(stdout), stdout: stdout, selection: selection}
	t.Cleanup(func() {
		if !s.waited {
			// Only this exact fixture-owned read-only client handle is signaled.
			_ = cmd.Process.Signal(syscall.SIGTERM)
			_ = cmd.Wait()
			s.waited = true
		}
	})
	return s
}

func (s *integratedCLIOutput) next(t *testing.T) integratedCLIFrame {
	t.Helper()
	var frame integratedCLIFrame
	if err := s.decoder.Decode(&frame); err != nil {
		t.Fatalf("integrated attach did not deliver a complete frame: %v", err)
	}
	if !reflect.DeepEqual(frame.Info.Selection, s.selection) {
		t.Fatal("integrated attach frame changed selection")
	}
	return frame
}

func (s *integratedCLIOutput) snapshot(t *testing.T) {
	t.Helper()
	f := s.next(t)
	if f.Kind != "snapshot" || f.Info.Ended {
		t.Fatal("attach was not established while original child was live")
	}
}

func (s *integratedCLIOutput) consume(t *testing.T, expected uint64, progress string) string {
	t.Helper()
	for s.cursor < expected {
		f := s.next(t)
		start, err1 := strconv.ParseUint(f.Start, 10, 64)
		end, err2 := strconv.ParseUint(f.End, 10, 64)
		if f.Kind != "data" || err1 != nil || err2 != nil ||
			f.Start != strconv.FormatUint(start, 10) || f.End != strconv.FormatUint(end, 10) ||
			start != s.cursor || end > expected || end <= start || end-start != uint64(len(f.Data)) ||
			!bytes.Equal(f.Data, bytes.Repeat([]byte("x"), len(f.Data))) {
			t.Fatalf("integrated output unavailable: kind=%s start=%s end=%s cursor=%d bytes=%d gap=%s/%s/%s", f.Kind, f.Start, f.End, s.cursor, len(f.Data), f.Gap.Reason, f.Gap.From, f.Gap.To)
		}
		s.cursor = end
		if err := os.WriteFile(progress, []byte(f.End), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return strconv.FormatUint(s.cursor, 10)
}

func (s *integratedCLIOutput) detach(t *testing.T) {
	t.Helper()
	if err := s.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	trailer := s.trailer(t)
	err := s.command.Wait()
	s.waited = true
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		signaled := ok && exit.ProcessState.Sys().(syscall.WaitStatus).Signaled() && exit.ProcessState.Sys().(syscall.WaitStatus).Signal() == syscall.SIGTERM
		canceled := ok && exit.ExitCode() == 1 && bytes.Contains(trailer, []byte("context canceled"))
		if !signaled && !canceled {
			t.Fatalf("read-only detach client actual Wait failed: %v %.500s", err, trailer)
		}
	}
}

func (s *integratedCLIOutput) ended(t *testing.T) {
	t.Helper()
	f := s.next(t)
	if f.Kind != "end" || !f.Info.Ended || len(f.Data) != 0 {
		t.Fatal("reconnected stream replayed bytes or failed to report actual end")
	}
	if trailer := s.trailer(t); !bytes.Equal(bytes.TrimSpace(trailer), []byte("PASS")) {
		t.Fatalf("unexpected synthetic CLI helper trailer: %.500s", trailer)
	}
	err := s.command.Wait()
	s.waited = true
	if err != nil {
		t.Fatalf("reconnected CLI did not actually exit normally: %v", err)
	}
}

func (s *integratedCLIOutput) replayAfterEviction(t *testing.T, cursor, base, head uint64) {
	t.Helper()
	f := s.next(t)
	if f.Kind != "snapshot" || !f.Info.Ended || f.Info.Base != strconv.FormatUint(base, 10) || f.Info.Head != strconv.FormatUint(head, 10) {
		t.Fatal("retained-output snapshot lost exact eviction bounds or actual capture end")
	}
	s.cursor = cursor
	if cursor < base {
		f = s.next(t)
		if f.Kind != "gap" || f.Gap.Reason != "evicted" || f.Gap.From != strconv.FormatUint(cursor, 10) || f.Gap.To != strconv.FormatUint(base, 10) || f.Gap.DeliveryUnknown == nil || *f.Gap.DeliveryUnknown {
			t.Fatal("known history eviction was hidden or confused with unknown live delivery")
		}
		s.cursor = base
	}
	for s.cursor < head {
		f = s.next(t)
		end := s.cursor + uint64(len(f.Data))
		if f.Kind != "data" || len(f.Data) == 0 || end > head || f.Start != strconv.FormatUint(s.cursor, 10) || f.End != strconv.FormatUint(end, 10) || !bytes.Equal(f.Data, bytes.Repeat([]byte("x"), len(f.Data))) {
			t.Fatal("retained output replay had a gap, duplicate, changed bytes or noncanonical cursor")
		}
		s.cursor = end
	}
	s.ended(t)
}

// The finite Go subprocess helper appends PASS or its cancellation diagnostic.
func (s *integratedCLIOutput) trailer(t *testing.T) []byte {
	t.Helper()
	data, err := io.ReadAll(io.LimitReader(io.MultiReader(s.decoder.Buffered(), s.stdout), 16385))
	if err != nil || len(data) > 16384 {
		t.Fatal("unavailable/oversized synthetic CLI helper trailer")
	}
	return data
}

func integratedPreviewCancelArgs(t *testing.T, root string, selection map[string]string, preview map[string]any) []string {
	t.Helper()
	rows, ok := preview["runs"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatal("integrated preview lost its one owned child")
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatal("invalid integrated preview row")
	}
	got, ok := row["selection"].(map[string]any)
	if !ok || len(got) != len(selection) || row["state"] != "live" || row["publication_uncertain"] != false {
		t.Fatal("integrated preview lost actual live selection")
	}
	for k, want := range selection {
		if got[k] != want {
			t.Fatal("integrated preview retargeted the native child")
		}
	}
	binding, ok := preview["binding"].(map[string]any)
	if !ok || binding["epoch"] != selection["epoch"] {
		t.Fatal("integrated preview epoch changed")
	}
	args := []string{"daemon", "shutdown", "cancel", "--runtime-dir", root}
	for _, k := range []string{"epoch", "preview_id", "revision", "roster_digest"} {
		v, ok := binding[k].(string)
		if !ok || v == "" {
			t.Fatal("integrated preview lost exact binding")
		}
		args = append(args, "--"+strings.ReplaceAll(k, "_", "-"), v)
	}
	for _, k := range []string{"total_runs", "pending_launches", "pending_controls", "pending_reconciliations"} {
		want := 0
		if k == "total_runs" {
			want = 1
		}
		if binding[k] != float64(want) {
			t.Fatal("integrated preview contained unexpected pending work")
		}
		args = append(args, "--"+strings.ReplaceAll(k, "_", "-"), strconv.Itoa(want))
	}
	return args
}

// The daemon's original exec.Cmd has already been Waited. Retired selection
// metadata cannot authorize starting a new owner or silently accepting effects.
func assertAbsentDaemonCLIRefusals(t *testing.T, command func(...string) *exec.Cmd, root string, selectionArgs []string) {
	t.Helper()
	snapshot := func() map[string]string {
		t.Helper()
		files := make(map[string]string)
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return fmt.Errorf("walk absent-daemon fixture: %w", err)
			}
			if len(files) >= 64 {
				t.Fatal("oversized absent-daemon metadata fixture")
			}
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("inspect absent-daemon fixture: %w", err)
			}
			if info.IsDir() {
				files[path] = info.Mode().String()
				return nil
			}
			if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
				t.Fatal("unsafe/unbounded absent-daemon metadata fixture")
			}
			file, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("open absent-daemon fixture: %w", err)
			}
			data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
			_ = file.Close()
			if err != nil {
				return fmt.Errorf("read absent-daemon fixture: %w", err)
			}
			if len(data) > 1024*1024 {
				t.Fatal("absent-daemon metadata grew beyond fixture bound")
			}
			hash := sha256.Sum256(data)
			files[path] = info.Mode().String() + string(hash[:])
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	if _, err := os.Lstat(filepath.Join(root, "daemon.sock")); !os.IsNotExist(err) {
		t.Fatal("fixture owner endpoint remained after actual daemon Wait")
	}
	before := snapshot()
	for _, test := range []struct {
		kind  string
		extra []string
	}{
		{"attach", []string{"--cursor", "0"}},
		{"input", []string{"--request-id", "absent-input", "--manual-terminal"}},
		{"resize", []string{"--request-id", "absent-resize", "--sequence", "2", "--rows", "32", "--cols", "102"}},
		{"stop", []string{"--request-id", "absent-stop", "--acknowledge-work-loss"}},
		{"observe", []string{"--host", "codex", "--host-version", "fixture-runtime 1", "--event-id", "spool:absent-owner", "--kind", "prompt_boundary"}},
	} {
		args := append([]string{"runtime", test.kind}, selectionArgs...)
		cmd := command(append(args, test.extra...)...)
		payload := []byte("bad!!")
		if test.kind == "input" {
			cmd.Stdin = bytes.NewReader(payload)
		}
		out, err := cmd.CombinedOutput()
		if bytes.Contains(out, payload) {
			t.Fatalf("absent daemon %s accepted a request or echoed input", test.kind)
		}
		if test.kind == "observe" {
			// Optional hook delivery deliberately succeeds without an owner, but
			// may report only unavailable/unbound admission, never queued state.
			var outcome map[string]any
			for _, line := range bytes.Split(out, []byte("\n")) {
				if len(line) > 0 && line[0] == '{' {
					if decodeErr := json.Unmarshal(line, &outcome); decodeErr != nil {
						t.Fatal(decodeErr)
					}
					break
				}
			}
			if err != nil || len(outcome) != 4 || outcome["schema_version"] != float64(1) || outcome["outcome"] != "unavailable" || outcome["binding"] != "unbound" || outcome["authority"] != "admission_only" {
				t.Fatal("absent observation invented delivery or broke best-effort compatibility")
			}
		} else if err == nil {
			t.Fatalf("absent daemon %s accepted a request", test.kind)
		}
		if !reflect.DeepEqual(snapshot(), before) {
			t.Fatalf("absent daemon %s mutated runtime metadata or started an owner", test.kind)
		}
	}
}
