//go:build darwin || linux

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strconv"
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
	} `json:"info"`
	Start string `json:"start"`
	End   string `json:"end"`
	Data  []byte `json:"data"`
	Gap   struct {
		Reason string `json:"reason"`
		From   string `json:"from"`
		To     string `json:"to"`
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

// The finite Go subprocess helper appends PASS or its cancellation diagnostic.
func (s *integratedCLIOutput) trailer(t *testing.T) []byte {
	t.Helper()
	data, err := io.ReadAll(io.LimitReader(io.MultiReader(s.decoder.Buffered(), s.stdout), 16385))
	if err != nil || len(data) > 16384 {
		t.Fatal("unavailable/oversized synthetic CLI helper trailer")
	}
	return data
}
