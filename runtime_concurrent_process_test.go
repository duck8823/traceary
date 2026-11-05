//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

// Synthetic children share a target, not control authority or artifact paths.
// The existing daemon fixture owns their handles; this helper never adopts PIDs.
type concurrentCLIRuns struct {
	root, epoch, worktree, resultBase string
	project, store                    string
	command                           func(...string) *exec.Cmd
	jsonCommand                       func(...string) map[string]any
	client                            *infra.LocalDaemon
}

func (f concurrentCLIRuns) verify(ctx context.Context, t *testing.T, first map[string]any) {
	t.Helper()
	second := f.jsonCommand("runtime", "launch", "--runtime-dir", f.root, "--epoch", f.epoch, "--worktree", f.worktree, "--profile", "codex-fixture", "--request-id", "explicit-second-fixture")
	selections := make([]app.OutputSelection, 2)
	paths := make([]string, 2)
	for i, receipt := range []map[string]any{first, second} {
		run := receipt["run"].(map[string]any)
		selections[i] = app.OutputSelection{Identity: domain.LaunchIdentity{Epoch: f.epoch, Run: run["run"].(string), Generation: run["generation"].(string)}, Worktree: domain.WorktreeID(f.worktree), Profile: "codex-fixture"}
		paths[i] = f.resultBase + "." + selections[i].Identity.Run
		_ = concurrentArtifact(t, paths[i]+".ready")
	}
	if selections[0].Identity.Run == selections[1].Identity.Run || selections[0].Identity.Generation == selections[1].Identity.Generation {
		t.Fatal("same-cwd children did not receive distinct identities")
	}
	assertStates := func(firstState, secondState domain.RunState) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for {
			page, err := f.client.ListRuns(ctx, f.root, app.RunPageRequest{Epoch: f.epoch})
			matched := 0
			if err == nil && len(page.Runs) == 2 {
				for i, selection := range selections {
					for _, run := range page.Runs {
						if run.Identity == selection.Identity && run.State == []domain.RunState{firstState, secondState}[i] {
							if run.State == domain.RunExited && run.ExitCode != -1 {
								t.Fatal("explicit Stop was replaced by finite child expiry")
							}
							matched++
						}
					}
				}
			}
			if matched == 2 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("actual child reconciliation lost concurrent selection/state: %v", err)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	assertStates(domain.RunLive, domain.RunLive)
	args := func(selection app.OutputSelection, kind, id string, extra ...string) []string {
		v := []string{"runtime", kind, "--runtime-dir", f.root, "--epoch", selection.Identity.Epoch, "--run", selection.Identity.Run, "--generation", selection.Identity.Generation, "--worktree", f.worktree, "--profile", "codex-fixture", "--request-id", id}
		return append(v, extra...)
	}
	foreign := selections[0]
	foreign.Identity.Generation = selections[1].Identity.Generation
	bad := f.command(args(foreign, "input", "crossed-real-generation", "--manual-terminal")...)
	bad.Stdin = bytes.NewBufferString("bad!!")
	out, err := bad.CombinedOutput()
	if err == nil || bytes.Contains(out, []byte("bad!!")) || !bytes.Contains(out, []byte(`"outcome":"rejected"`)) || !bytes.Contains(out, []byte(`"written_bytes":"0"`)) {
		t.Fatal("crossed existing generation was not safely rejected")
	}
	for i, selection := range selections {
		if _, err := f.client.Receipt(ctx, f.root, selection, "crossed-real-generation"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("crossed generation polluted a real receipt journal: %v", err)
		}
		if _, err := os.Stat(paths[i] + ".input"); !os.IsNotExist(err) {
			t.Fatal("crossed generation delivered bytes to a real child")
		}
	}
	for i, selection := range selections {
		payload := []byte([]string{"AAAAA", "BBBBB"}[i])
		for range 2 { // Same request, exact duplicate: inspect, never replay.
			cmd := f.command(args(selection, "input", "shared-request-id", "--manual-terminal")...)
			cmd.Stdin = bytes.NewReader(payload)
			out, err := cmd.CombinedOutput()
			if err != nil || bytes.Contains(out, payload) || !bytes.Contains(out, []byte(`"written_bytes":"5"`)) {
				t.Fatal("same request ID across distinct selections lost exact receipt")
			}
		}
		receipt, err := f.client.Receipt(ctx, f.root, selection, "shared-request-id")
		if err != nil || receipt.Selection != selection || receipt.Receipt.WrittenBytes != 5 || receipt.Receipt.Outcome != domain.ControlDelivered {
			t.Fatalf("concurrent input receipt belongs to wrong selection: %v", err)
		}
		if !bytes.Equal(concurrentArtifact(t, paths[i]+".input"), payload) {
			t.Fatal("same-cwd child received another selection's input")
		}
		rows, cols := strconv.Itoa(31+i), strconv.Itoa(101+i)
		_ = f.jsonCommand(args(selection, "resize", "shared-resize-id", "--sequence", "1", "--rows", rows, "--cols", cols)...)
		marker := f.command(args(selection, "input", "shared-marker-id", "--manual-terminal")...)
		marker.Stdin = bytes.NewBufferString("r")
		if out, err := marker.CombinedOutput(); err != nil {
			t.Fatalf("concurrent marker: %v %.500s", err, out)
		}
		if string(concurrentArtifact(t, paths[i]+".geometry")) != "["+rows+","+cols+"]" {
			t.Fatal("resize crossed selection or duplicate input was replayed")
		}
		var native map[string]string
		if err := json.Unmarshal(concurrentArtifact(t, paths[i]), &native); err != nil || native["run"] != selection.Identity.Run || native["cwd"] != f.project || native["store"] != f.store {
			t.Fatal("run-specific child artifact lost original identity")
		}
	}
	assertStates(domain.RunLive, domain.RunLive)
	_ = f.jsonCommand(args(selections[0], "stop", "shared-stop-id", "--acknowledge-work-loss")...)
	assertStates(domain.RunExited, domain.RunLive)
	_ = f.jsonCommand(args(selections[1], "stop", "shared-stop-id", "--acknowledge-work-loss")...)
	assertStates(domain.RunExited, domain.RunExited)
}

func concurrentArtifact(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			return data
		}
		if !os.IsNotExist(err) || time.Now().After(deadline) {
			t.Fatalf("concurrent child artifact unavailable: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
