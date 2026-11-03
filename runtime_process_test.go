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
	"strings"
	"syscall"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

func TestRuntimeCLIEndToEndRegistrationLaunchAndListWithoutHomeOrCoreBootstrap(t *testing.T) {
	for _, control := range []bool{false, true} {
		name := "output"
		if control {
			name = "controls"
		}
		t.Run(name, func(t *testing.T) { runtimeCLIProcessFixture(t, control) })
	}
}
func runtimeCLIProcessFixture(t *testing.T, control bool) {
	t.Helper()
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
import("os";"encoding/json";"bytes";"io";"time";"golang.org/x/term";"golang.org/x/sys/unix")
func main(){
 if len(os.Args)==2&&os.Args[1]=="--version"{os.Stdout.WriteString("fixture-runtime 1\n");return}
 if len(os.Args)!=1{os.Exit(93)}
 cwd,_:=os.Getwd();data,_:=json.Marshal(map[string]string{"cwd":cwd,"store":os.Getenv("TRACEARY_DB_PATH"),"run":os.Getenv("TRACEARY_RUNTIME_RUN_ID")})
 result:=os.Getenv("TRACEARY_TEST_RUNTIME_RESULT")
 if os.WriteFile(result,data,0600)!=nil{os.Exit(90)}
 control:=os.Getenv("TRACEARY_TEST_RUNTIME_CONTROL")=="1"
 if control {if _,e:=term.MakeRaw(0);e!=nil{os.Exit(94)};os.WriteFile(result+".ready",[]byte("ready"),0600)}
 os.Stdout.Write(bytes.Repeat([]byte("x"),2*1024*1024))
 if control {
  raw:=make([]byte,5);if _,e:=io.ReadFull(os.Stdin,raw);e!=nil{os.Exit(95)};os.WriteFile(result+".input",raw,0600)
  marker:=make([]byte,1);if _,e:=io.ReadFull(os.Stdin,marker);e!=nil{os.Exit(95)}
  size,e:=unix.IoctlGetWinsize(0,unix.TIOCGWINSZ);if e!=nil{os.Exit(96)};geometry,_:=json.Marshal([]uint16{size.Row,size.Col});os.WriteFile(result+".geometry",geometry,0600)
  time.Sleep(15*time.Second);os.Exit(92)
 }
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
		if control {
			cmd.Env = append(cmd.Env, "TRACEARY_TEST_RUNTIME_CONTROL=1")
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
	launched := jsonCommand("runtime", "launch", "--runtime-dir", metadata, "--epoch", status.Epoch, "--worktree", w["id"].(string), "--profile", "codex-fixture", "--request-id", "explicit-fixture-request")
	if launched["request_id"] != "explicit-fixture-request" {
		t.Fatal("launch receipt lost request")
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
