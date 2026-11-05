//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

func TestRuntimeCLIEndToEndRegistrationLaunchAndListWithoutHomeOrCoreBootstrap(t *testing.T) {
	for _, mode := range []string{"output", "controls", "observe", "shutdown", "crash", "update", "autostart"} {
		t.Run(mode, func(t *testing.T) { runtimeCLIProcessFixture(t, mode) })
	}
}
func runtimeCLIProcessFixture(t *testing.T, mode string) {
	t.Helper()
	control := mode == "controls"
	lifecycle := mode == "shutdown" || mode == "crash"

	base := os.TempDir()
	if runtime.GOOS == "darwin" {
		base = "/private/tmp"
	}
	root, err := os.MkdirTemp(base, "tcl-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(root, "rt")
	project := filepath.Join(root, "project")
	if err = os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	git := exec.Command("git", "-C", project, "init", "--quiet")
	git.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
	if out, e := git.CombinedOutput(); e != nil {
		t.Fatalf("synthetic Git: %v %s", e, out)
	}
	store := filepath.Join(root, "existing-core.db")
	original := []byte("synthetic store identity, not an actual SQLite store")
	if err = os.WriteFile(store, original, 0600); err != nil {
		t.Fatal(err)
	}
	resultFile := filepath.Join(root, "agent-result.json")
	agentSource := filepath.Join(root, "fixture-agent.go")
	agentBinary := filepath.Join(root, "fixture-agent")
	program := `package main
import("os";"encoding/json";"bytes";"io";"time";"os/signal";"syscall";"golang.org/x/term";"golang.org/x/sys/unix")
func main(){
 if len(os.Args)==2&&os.Args[1]=="--version"{os.Stdout.WriteString("fixture-runtime 1\n");return}
 if len(os.Args)!=1{os.Exit(93)}
 cwd,_:=os.Getwd();data,_:=json.Marshal(map[string]string{"cwd":cwd,"store":os.Getenv("TRACEARY_DB_PATH"),"run":os.Getenv("TRACEARY_RUNTIME_RUN_ID")})
 result:=os.Getenv("TRACEARY_TEST_RUNTIME_RESULT")
 if os.WriteFile(result,data,0600)!=nil{os.Exit(90)}
 lifecycle:=os.Getenv("TRACEARY_TEST_RUNTIME_SHUTDOWN")=="1"
 if lifecycle {signal.Ignore(syscall.SIGHUP);os.WriteFile(result+".ready",[]byte("ready"),0600)}
 control:=os.Getenv("TRACEARY_TEST_RUNTIME_CONTROL")=="1"
 if control {if _,e:=term.MakeRaw(0);e!=nil{os.Exit(94)};os.WriteFile(result+".ready",[]byte("ready"),0600)}
 os.Stdout.Write(bytes.Repeat([]byte("x"),2*1024*1024))
 if os.Getenv("TRACEARY_TEST_RUNTIME_OBSERVE")=="1" {
  deadline:=time.Now().Add(15*time.Second)
  for {if _,e:=os.Stat(result+".observe-release");e==nil{break};if time.Now().After(deadline){os.Exit(92)};time.Sleep(5*time.Millisecond)}
 }

 if control {
  raw:=make([]byte,5);if _,e:=io.ReadFull(os.Stdin,raw);e!=nil{os.Exit(95)};os.WriteFile(result+".input",raw,0600)
  marker:=make([]byte,1);if _,e:=io.ReadFull(os.Stdin,marker);e!=nil{os.Exit(95)}
  size,e:=unix.IoctlGetWinsize(0,unix.TIOCGWINSZ);if e!=nil{os.Exit(96)};geometry,_:=json.Marshal([]uint16{size.Row,size.Col});os.WriteFile(result+".geometry",geometry,0600)
  time.Sleep(15*time.Second);os.Exit(92)
 }
 if lifecycle {time.Sleep(15*time.Second);os.WriteFile(result+".survived",[]byte("survived"),0600);os.Exit(92)}
 os.Exit(7)
}
`
	if err = os.WriteFile(agentSource, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-mod=readonly", "-o", agentBinary, agentSource)
	build.Env = append(os.Environ(), "GOENV=off", "GOWORK=off", "GO111MODULE=on", "GOPROXY=off")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build owned synthetic native agent: %v %.1000s", e, out)
	}

	poisonCore := filepath.Join(root, "must-not-create", "core.db")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := func(args ...string) *exec.Cmd {
		data, _ := json.Marshal(args)
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestDaemonCommandProcessHelper$")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TRACEARY_LANG=en", "LOG_LEVEL=error", "TRACEARY_TEST_DAEMON_HELPER=1", "TRACEARY_TEST_DAEMON_ARGS=" + string(data), "TRACEARY_TEST_RUNTIME_RESULT=" + resultFile, "TRACEARY_DB_PATH=" + poisonCore, "GORACE=atexit_sleep_ms=0"}
		if lifecycle {
			cmd.Env = append(cmd.Env, "TRACEARY_TEST_RUNTIME_SHUTDOWN=1")
		}
		if control {
			cmd.Env = append(cmd.Env, "TRACEARY_TEST_RUNTIME_CONTROL=1")
		}
		if mode == "observe" {
			cmd.Env = append(cmd.Env, "TRACEARY_TEST_RUNTIME_OBSERVE=1")
		}

		return cmd
	}
	jsonCommand := func(args ...string) map[string]any {
		t.Helper()
		out, e := command(args...).CombinedOutput()
		if e != nil {
			t.Fatalf("runtime fixture command failed: %v %.2000s", e, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "{") {
				var result map[string]any
				if e = json.Unmarshal([]byte(line), &result); e != nil {
					t.Fatal(e)
				}
				return result
			}
		}
		t.Fatal("runtime command did not emit JSON")
		return nil
	}
	missing := filepath.Join(root, "not-created")
	if mode == "autostart" {
		for _, args := range [][]string{{"status"}, {"descriptor"}, {"opt-in"}} {
			args = append([]string{"daemon", "autostart", "--runtime-dir", missing}, args...)
			if e := command(args...).Run(); e == nil {
				t.Fatal("missing root/explicit flag accepted")
			}
			if _, e := os.Lstat(missing); !os.IsNotExist(e) {
				t.Fatal("read/missing flag created root")
			}
		}
		for _, value := range []string{"false", "true"} {
			view := jsonCommand("daemon", "autostart", "opt-in", "--runtime-dir", metadata, "--enabled="+value)
			if view["opt_in"] != (value == "true") || view["installation_state"] != "unknown" || view["host_inspection"] != "not_performed" || view["starts_agents"] != false {
				t.Fatal("preference invented host installation", view)
			}
		}
		view := jsonCommand("daemon", "autostart", "status", "--runtime-dir", metadata)
		if view["opt_in"] != true {
			t.Fatal("CLI lost preference")
		}
		out, e := command("daemon", "autostart", "descriptor", "--runtime-dir", metadata).CombinedOutput()
		if runtime.GOOS == "darwin" {
			actualBinary, pathErr := filepath.EvalSymlinks(binary)
			if e != nil || pathErr != nil || !bytes.Contains(out, []byte("<string>"+actualBinary+"</string>")) || !bytes.Contains(out, []byte("<string>daemon</string>")) || !bytes.Contains(out, []byte("<string>serve</string>")) {
				t.Fatalf("actual CLI descriptor failed: %v %.1000s", e, out)
			}
		} else if e == nil {
			t.Fatal("unsupported descriptor unexpectedly succeeded")
		}
		actual, e := os.ReadFile(store)
		if e != nil || !bytes.Equal(actual, original) {
			t.Fatal("auto-start changed core store")
		}
		for _, path := range []string{filepath.Dir(poisonCore), resultFile, filepath.Join(metadata, "daemon.sock"), filepath.Join(metadata, "observations.sock"), filepath.Join(metadata, "execution.json"), filepath.Join(metadata, "registry.json")} {
			if _, e = os.Stat(path); !os.IsNotExist(e) {
				t.Fatal("auto-start created core/agent/daemon/registry state", path, e)
			}
		}
		return
	}
	if e := command("runtime", "registry", "list", "--runtime-dir", missing).Run(); e == nil {
		t.Fatal("missing registry silently initialized")
	}
	if _, e := os.Stat(missing); !os.IsNotExist(e) {
		t.Fatal("read created missing runtime root")
	}
	p := jsonCommand("runtime", "project", "register", "--runtime-dir", metadata, "--path", project)
	w := jsonCommand("runtime", "worktree", "register", "--runtime-dir", metadata, "--project", p["id"].(string), "--path", project, "--db-path", store)
	profile := jsonCommand("runtime", "profile", "register", "--runtime-dir", metadata, "--id", "codex-fixture", "--host", "codex", "--executable", agentBinary, "--expected-version", "fixture-runtime 1")
	if profile["id"] != "codex-fixture" {
		t.Fatal("profile registration lost policy")
	}
	server := command("daemon", "serve", "--runtime-dir", metadata)
	var errors bytes.Buffer
	server.Stderr = &errors
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Wait(); close(done) }()
	defer func() {
		select {
		case <-done:
			return
		default:
		}
		_ = server.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = server.Process.Kill()
			<-done
		}
	}()
	client, err := infra.NewLocalDaemon(nil, infra.DaemonOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var status app.DaemonStatus
	deadline := time.Now().Add(4 * time.Second)
	for {
		status, err = client.Status(ctx, metadata)
		if err == nil {
			break
		}
		select {
		case e := <-done:
			t.Fatalf("daemon exited: %v %.2000s", e, errors.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon never ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if mode == "update" {
		view := jsonCommand("daemon", "update", "status", "--runtime-dir", metadata, "--epoch", status.Epoch)
		if view["quiescent"] != true || view["authority"] != "current_owner_metadata_only_not_installation" {
			t.Fatal("CLI update status invented permission or work")
		}
		attempt := jsonCommand("daemon", "update", "attempt", "--runtime-dir", metadata, "--epoch", status.Epoch, "--target-version", "fixture-v2", "--target-protocol", "2")
		if attempt["notice"] != true || attempt["outcome"] != "quiescent_snapshot_not_permission" {
			t.Fatal("CLI attempt lost notice/boundary")
		}
		b := attempt["preview"].(map[string]any)
		args := []string{"daemon", "update", "handoff", "--runtime-dir", metadata, "--epoch", status.Epoch, "--target-version", "fixture-v2", "--target-protocol", "2", "--preview-id", b["preview_id"].(string), "--revision", b["revision"].(string), "--roster-digest", b["roster_digest"].(string), "--total-runs", "0", "--pending-launches", "0", "--pending-controls", "0", "--pending-reconciliations", "0"}
		if e := command(args...).Run(); e == nil {
			t.Fatal("actual CLI handoff accepted missing exit acknowledgment")
		}
		out := jsonCommand(append(args, "--acknowledge-daemon-exit")...)
		if out["outcome"] != "draining" || out["closed_terminals"] != float64(0) {
			t.Fatal("empty CLI handoff changed live work")
		}
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-ctx.Done():
			t.Fatal("actual CLI update handoff did not exit")
		}
		history := jsonCommand("daemon", "shutdown", "history", "--runtime-dir", metadata)
		if history["clean"] != true {
			t.Fatal("known empty CLI handoff not clean")
		}
		actual, e := os.ReadFile(store)
		if e != nil || !bytes.Equal(actual, original) {
			t.Fatal("CLI update changed core store")
		}
		if _, e = os.Stat(filepath.Dir(poisonCore)); !os.IsNotExist(e) {
			t.Fatal("CLI update bootstrapped core path")
		}
		if _, e = os.Stat(resultFile); !os.IsNotExist(e) {
			t.Fatal("CLI update launched native agent")
		}
		return
	}
	launched := jsonCommand("runtime", "launch", "--runtime-dir", metadata, "--epoch", status.Epoch, "--worktree", w["id"].(string), "--profile", "codex-fixture", "--request-id", "explicit-fixture-request")
	if launched["request_id"] != "explicit-fixture-request" {
		t.Fatal("launch receipt lost request")
	}
	if lifecycle {
		ready := time.Now().Add(4 * time.Second)
		for {
			if _, e := os.Stat(resultFile + ".ready"); e == nil {
				break
			}
			if time.Now().After(ready) {
				t.Fatal("lifecycle native fixture not ready")
			}
			time.Sleep(5 * time.Millisecond)
		}
		target := launched["run"].(map[string]any)
		if mode == "shutdown" {
			deferred := jsonCommand("daemon", "update", "attempt", "--runtime-dir", metadata, "--epoch", status.Epoch, "--target-version", "fixture-v2", "--target-protocol", "2")
			if deferred["notice"] != true || deferred["outcome"] != "deferred" {
				t.Fatal("active CLI update did not defer")
			}
			preview := jsonCommand("daemon", "shutdown", "preview", "--runtime-dir", metadata, "--epoch", status.Epoch)
			bindingArgs := func(kind string, p map[string]any) []string {
				b := p["binding"].(map[string]any)
				v := []string{"daemon", "shutdown", kind, "--runtime-dir", metadata, "--epoch", b["epoch"].(string), "--preview-id", b["preview_id"].(string), "--revision", b["revision"].(string), "--roster-digest", b["roster_digest"].(string)}
				for _, name := range []string{"total_runs", "pending_launches", "pending_controls", "pending_reconciliations"} {
					v = append(v, "--"+strings.ReplaceAll(name, "_", "-"), strconv.Itoa(int(b[name].(float64))))
				}
				return v
			}
			if len(preview["runs"].([]any)) != 1 {
				t.Fatal("actual CLI omitted affected run")
			}
			_ = jsonCommand(bindingArgs("cancel", preview)...)
			preview = jsonCommand("daemon", "shutdown", "preview", "--runtime-dir", metadata, "--epoch", status.Epoch)
			if e := command(bindingArgs("confirm", preview)...).Run(); e == nil {
				t.Fatal("actual CLI confirmed without work-loss acknowledgment")
			}
			confirmation := jsonCommand(append(bindingArgs("confirm", preview), "--acknowledge-work-loss")...)
			if confirmation["outcome"] != "draining" {
				t.Fatal("actual CLI did not confirm master closure")
			}
			receipt := jsonCommand(bindingArgs("receipt", preview)...)
			if receipt["outcome"] != "draining" {
				t.Fatal("actual CLI receipt lost decision")
			}
			stopped := command("runtime", "stop", "--runtime-dir", metadata, "--epoch", status.Epoch, "--run", target["run"].(string), "--generation", target["generation"].(string), "--worktree", w["id"].(string), "--profile", "codex-fixture", "--request-id", "explicit-after-master-close", "--acknowledge-work-loss")
			output, e := stopped.CombinedOutput()
			if e != nil && !bytes.Contains(output, []byte(`"outcome":"unknown"`)) {
				t.Fatalf("actual CLI Stop failed during shutdown: %v %.1000s", e, output)
			}
			select {
			case e = <-done:
				if e != nil {
					t.Fatalf("confirmed daemon exit: %v %.1000s", e, errors.String())
				}
			case <-ctx.Done():
				t.Fatal("confirmed daemon failed to exit")
			}
			history := jsonCommand("daemon", "shutdown", "history", "--runtime-dir", metadata)
			if history["clean"] != true || history["authority"] != "history_only_not_resume_permission" || len(history["decisions"].([]any)) != 1 {
				t.Fatal("offline CLI history lost clean marker or invented resume authority")
			}
		} else {
			// SIGKILL targets only this test-owned daemon handle. Never signal or adopt
			// the old native child/PID; it is finite and explicitly ignores SIGHUP.
			if e := server.Process.Kill(); e != nil {
				t.Fatal(e)
			}
			if e := <-done; e == nil {
				t.Fatal("crash fixture did not interrupt daemon")
			}
			restarted := command("daemon", "serve", "--runtime-dir", metadata)
			var restartErrors bytes.Buffer
			restarted.Stderr = &restartErrors
			restarted.Stdout = &restartErrors
			if e := restarted.Start(); e != nil {
				t.Fatal(e)
			}
			restartDone := make(chan error, 1)
			go func() { restartDone <- restarted.Wait(); close(restartDone) }()
			defer func() {
				_ = restarted.Process.Signal(syscall.SIGTERM)
				select {
				case <-restartDone:
				case <-time.After(3 * time.Second):
					_ = restarted.Process.Kill()
					<-restartDone
				}
			}()
			limit := time.Now().Add(4 * time.Second)
			var fresh app.DaemonStatus
			for {
				fresh, err = client.Status(ctx, metadata)
				if err == nil && fresh.Epoch != status.Epoch {
					break
				}
				select {
				case e := <-restartDone:
					t.Fatalf("restart exited: %v %.1000s", e, restartErrors.String())
				default:
				}
				if time.Now().After(limit) {
					t.Fatal("restart did not issue new epoch")
				}
				time.Sleep(5 * time.Millisecond)
			}
			history := jsonCommand("daemon", "shutdown", "history", "--runtime-dir", metadata)
			old := history["interrupted"].([]any)
			if history["previous_epoch"] != status.Epoch || history["previous_clean"] != false || len(old) != 1 || old[0].(map[string]any)["status"] != "orphaned" {
				t.Fatal("crash history falsely restored old process")
			}
			runs, e := client.ListRuns(ctx, metadata, app.RunPageRequest{Epoch: fresh.Epoch})
			if e != nil || len(runs.Runs) != 0 {
				t.Fatal("restart adopted old saved PID", e)
			}
			selection := app.OutputSelection{Identity: domain.LaunchIdentity{Epoch: status.Epoch, Run: target["run"].(string), Generation: target["generation"].(string)}, Worktree: domain.WorktreeID(w["id"].(string)), Profile: "codex-fixture"}
			intent, e := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "must-not-signal-old-owner", Kind: domain.ControlStop, WorkLossAcknowledged: true})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = client.Control(ctx, metadata, app.ControlRequest{Selection: selection, Intent: intent}); e == nil {
				t.Fatal("old epoch allowed a process effect")
			}
			// Survival proves restart did not silently kill/adopt the prior native child.
			for {
				if _, e := os.Stat(resultFile + ".survived"); e == nil {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("old finite native child did not survive crash/restart without adoption")
				default:
				}
				time.Sleep(10 * time.Millisecond)
			}
			if e = restarted.Process.Signal(syscall.SIGTERM); e != nil {
				t.Fatal(e)
			}
			select {
			case e = <-restartDone:
				if e != nil {
					t.Fatal(e)
				}
			case <-ctx.Done():
				t.Fatal("empty restarted owner did not stop")
			}
		}
		actual, e := os.ReadFile(store)
		if e != nil || !bytes.Equal(actual, original) {
			t.Fatal("lifecycle CLI changed core store")
		}
		if _, e = os.Stat(filepath.Dir(poisonCore)); !os.IsNotExist(e) {
			t.Fatal("lifecycle CLI bootstrapped core path")
		}
		return
	}

	if mode == "observe" {
		target := launched["run"].(map[string]any)
		args := []string{"runtime", "observe", "--runtime-dir", metadata, "--epoch", status.Epoch, "--run", target["run"].(string), "--generation", target["generation"].(string), "--worktree", w["id"].(string), "--profile", "codex-fixture", "--host", "codex", "--host-version", "fixture-runtime 1", "--event-id", "spool:actual-cli", "--kind", "prompt_boundary"}
		observed := jsonCommand(args...)
		if len(observed) != 4 || observed["outcome"] != "queued" || observed["binding"] != "unbound" || observed["authority"] != "admission_only" {
			t.Fatal("actual CLI did not deliver admission-only metadata", observed)
		}
		if err = os.WriteFile(resultFile+".observe-release", []byte("release finite owned fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	expectedExit := 7
	if control {
		expectedExit = -1
		target := launched["run"].(map[string]any)
		flags := []string{"--runtime-dir", metadata, "--epoch", status.Epoch, "--run", target["run"].(string), "--generation", target["generation"].(string), "--worktree", w["id"].(string), "--profile", "codex-fixture"}
		args := func(kind, id string, extra ...string) []string {
			v := append([]string{"runtime", kind}, flags...)
			v = append(v, "--request-id", id)
			return append(v, extra...)
		}
		artifact := func(path string) []byte {
			t.Helper()
			limit := time.Now().Add(4 * time.Second)
			for {
				data, e := os.ReadFile(path)
				if e == nil {
					return data
				}
				if time.Now().After(limit) {
					t.Fatal("actual CLI native fixture result missing")
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		_ = artifact(resultFile + ".ready")
		raw := []byte{0xe6, 0x97, 0xa5, 0x1b, 0xff}
		input := command(args("input", "cli-input", "--manual-terminal")...)
		input.Stdin = bytes.NewReader(raw)
		out, e := input.CombinedOutput()
		if e != nil || bytes.Contains(out, raw) || !bytes.Contains(out, []byte(`"written_bytes":"5"`)) {
			t.Fatalf("actual CLI stdin control failed: %v", e)
		}
		if !bytes.Equal(artifact(resultFile+".input"), raw) {
			t.Fatal("actual CLI changed bytes or appended Enter")
		}
		duplicate := command(args("input", "cli-input", "--manual-terminal")...)
		duplicate.Stdin = bytes.NewReader(raw)
		if _, e = duplicate.CombinedOutput(); e != nil {
			t.Fatal("actual CLI duplicate receipt inspection failed")
		}
		_ = jsonCommand(args("resize", "cli-resize", "--sequence", "1", "--rows", "31", "--cols", "101")...)
		marker := command(args("input", "cli-marker", "--manual-terminal")...)
		marker.Stdin = strings.NewReader("r")
		if _, e = marker.CombinedOutput(); e != nil {
			t.Fatal("actual CLI marker failed")
		}
		if string(artifact(resultFile+".geometry")) != "[31,101]" {
			t.Fatal("actual CLI resize or no-replay contract failed")
		}
		_ = jsonCommand(args("stop", "cli-stop", "--acknowledge-work-loss")...)
		receipt := jsonCommand(args("receipt", "cli-input")...)
		if receipt["receipt"].(map[string]any)["written_bytes"] != "5" {
			t.Fatal("actual CLI lost input receipt after stop")
		}
	}
	deadline = time.Now().Add(4 * time.Second)
	for {
		page, e := client.ListRuns(ctx, metadata, app.RunPageRequest{Epoch: status.Epoch})
		if e == nil && len(page.Runs) == 1 && page.Runs[0].State == domain.RunExited {
			if page.Runs[0].ExitCode != expectedExit {
				t.Fatal("owned child lost")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("owned synthetic exit not reconciled: %v", e)
		}
		time.Sleep(5 * time.Millisecond)
	}
	page := jsonCommand("runtime", "list", "--runtime-dir", metadata, "--epoch", status.Epoch)
	if len(page["runs"].([]any)) != 1 {
		t.Fatal("CLI list lost managed run")
	}
	runView := launched["run"].(map[string]any)
	attachArgs := []string{"runtime", "attach", "--runtime-dir", metadata, "--epoch", status.Epoch, "--run", runView["run"].(string), "--generation", runView["generation"].(string), "--worktree", w["id"].(string), "--profile", "codex-fixture"}
	attach := func(cursor string) (int, string) {
		t.Helper()
		out, e := command(append(append([]string{}, attachArgs...), "--cursor", cursor)...).CombinedOutput()
		if e != nil {
			t.Fatalf("actual CLI attach failed: %v", e)
		}
		var count int
		var last string
		var sawSnapshot, sawEnd bool
		for _, line := range bytes.Split(out, []byte("\n")) {
			if len(line) == 0 || line[0] != '{' {
				continue
			}
			var frame map[string]any
			if e = json.Unmarshal(line, &frame); e != nil {
				t.Fatal(e)
			}
			switch frame["kind"] {
			case "snapshot":
				sawSnapshot = true
			case "end":
				sawEnd = true
			case "data":
				data, e := base64.StdEncoding.DecodeString(frame["data"].(string))
				if e != nil {
					t.Fatal(e)
				}
				if !bytes.Equal(data, bytes.Repeat([]byte("x"), len(data))) {
					t.Fatal("actual CLI changed raw bytes")
				}
				count += len(data)
				last = frame["end"].(string)
			default:
				t.Fatal("unexpected actual CLI gap")
			}
		}
		if !sawSnapshot || !sawEnd {
			t.Fatal("actual CLI stream incomplete")
		}
		return count, last
	}
	count, last := attach("0")
	if count != 2*1024*1024 || last != "2097152" {
		t.Fatal("actual CLI lost native output")
	}
	if count, _ = attach(last); count != 0 {
		t.Fatal("actual CLI replayed already consumed bytes")
	}
	actual, err := os.ReadFile(store)
	if err != nil || !bytes.Equal(actual, original) {
		t.Fatal("runtime inspected or mutated core store contents")
	}
	if _, e := os.Stat(filepath.Dir(poisonCore)); !os.IsNotExist(e) {
		t.Fatal("runtime namespace bootstrapped core path")
	}
	actual, err = os.ReadFile(resultFile)
	if err != nil {
		t.Fatal(err)
	}
	var native map[string]string
	if err = json.Unmarshal(actual, &native); err != nil {
		t.Fatal(err)
	}
	if native["cwd"] != project || native["store"] != store || native["run"] == "" {
		t.Fatal("native direct process target mismatch")
	}
	if err = server.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatalf("graceful owner exit: %v %.2000s", err, errors.String())
		}
	case <-ctx.Done():
		t.Fatal("daemon did not exit after owned reconciliation")
	}
}
