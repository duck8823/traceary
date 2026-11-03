//go:build darwin || linux

package runtime_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

func TestManagedDaemonLaunchListAndCancelPreserveActualPTYOwner(t *testing.T) {
	_, descriptor, result := ptyFixture(t)
	ctx := context.Background()
	root := daemonRoot(t)
	services, err := (infra.LocalOperatorFactory{}).Create(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	project, err := services.Registry.RegisterProject(ctx, descriptor.Target().Worktree().Path())
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := services.Registry.RegisterWorktree(ctx, project.ID(), descriptor.Target().Worktree().Path(), descriptor.Target().Worktree().Store())
	if err != nil {
		t.Fatal(err)
	}
	journal, err := infra.OpenExecutionStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.RegisterProfile(ctx, descriptor.Snapshot().Profile); err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(filepath.Dir(result), "release-child")
	factory := func(_ context.Context, metadataRoot, epoch string) (*app.Runtime, error) {
		registryStore, e := infra.OpenRegistryStore(metadataRoot, infra.MaxRegistryBytes)
		if e != nil {
			return nil, fmt.Errorf("compose synthetic owner: %w", e)
		}
		registry, e := app.NewRegistry(registryStore, infra.RegistryResolver{})
		if e != nil {
			return nil, fmt.Errorf("compose synthetic owner: %w", e)
		}
		launcher, e := infra.NewPTYLauncher(registry, []string{"TRACEARY_PTY_FIXTURE=1", "FIXTURE_RESULT=" + result, "FIXTURE_RELEASE=" + release, "FIXTURE_VERSION_DELAY=1200ms", "GORACE=atexit_sleep_ms=0"})
		if e != nil {
			return nil, fmt.Errorf("compose synthetic owner: %w", e)
		}
		return app.NewRuntime(registry, journal, launcher, journal, epoch)
	}
	d := daemon(t, nil, infra.DaemonOptions{RuntimeFactory: factory})
	cancel, done := startDaemon(t, d, root)
	t.Cleanup(func() { _ = os.WriteFile(release, []byte("release owned child"), 0600) })
	status, err := d.Status(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	req := app.LaunchRequest{Epoch: status.Epoch, Worktree: worktree.ID(), Profile: descriptor.Target().Profile(), RequestID: "managed-pty"}
	observation := rawEndpoint(t, root, "observations.sock", "observations")
	forbidden, _ := json.Marshal(map[string]any{"protocol_version": 1, "request_id": "no-role-escalation", "operation": "launch", "launch": map[string]any{"epoch": status.Epoch, "worktree": string(worktree.ID()), "profile": string(descriptor.Target().Profile()), "request_id": "forbidden"}})
	rawFrame(t, observation, string(forbidden))
	if rawCode(t, observation) != "unsupported" {
		t.Fatal("observation endpoint allowed process launch")
	}
	management := rawEndpoint(t, root, "daemon.sock", "management")
	forbidden, _ = json.Marshal(map[string]any{"protocol_version": 1, "request_id": "no-command-override", "operation": "launch", "launch": map[string]any{"epoch": status.Epoch, "worktree": string(worktree.ID()), "profile": string(descriptor.Target().Profile()), "request_id": "forbidden", "executable": "/bin/sh"}})
	rawFrame(t, management, string(forbidden))
	if rawCode(t, management) != "invalid" {
		t.Fatal("launch allowed arbitrary executable field")
	}
	if records, e := journal.ListLaunches(ctx); e != nil || len(records) != 0 {
		t.Fatal("rejected role/schema mutated launch journal")
	}

	run, err := d.Launch(ctx, root, req)
	if err != nil {
		t.Fatal(err)
	}
	page, err := d.ListRuns(ctx, root, app.RunPageRequest{Epoch: status.Epoch})
	if err != nil || len(page.Runs) != 1 || page.Runs[0].Identity != run.Identity {
		t.Fatalf("typed list: %v", err)
	}
	cancel()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s, e := d.Status(ctx, root)
		if e == nil && s.State == "draining" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("active cancellation did not preserve management endpoint")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("Serve exited and abandoned a live PTY")
	default:
	}
	other := req
	other.RequestID = "not-admitted"
	if _, err = d.Launch(ctx, root, other); !errors.Is(err, app.ErrRuntimeBusy) {
		t.Fatalf("draining admitted launch: %v", err)
	}
	if err = os.WriteFile(release, []byte("release owned child"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owned exit did not complete foreground drain")
	}
	records, err := journal.ListLaunches(ctx)
	if err != nil || len(records) != 1 || records[0].State != domain.RunExited || records[0].ExitCode != 7 {
		t.Fatalf("child was killed or lost: %v", err)
	}
}

func TestManagedLaunchLostAcknowledgementIsUnknownWithoutReplay(t *testing.T) {
	root := daemonRoot(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(root, "daemon.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	defer func() { _ = listener.Close() }()
	if err = os.Chmod(filepath.Join(root, "daemon.sock"), 0600); err != nil {
		t.Fatal(err)
	}
	seen := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, e := listener.AcceptUnix()
		if e != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		read := func() map[string]any {
			var prefix [4]byte
			if _, e := io.ReadFull(conn, prefix[:]); e != nil {
				return nil
			}
			n := binary.BigEndian.Uint32(prefix[:])
			if n > 65536 {
				return nil
			}
			data := make([]byte, n)
			if _, e := io.ReadFull(conn, data); e != nil {
				return nil
			}
			var value map[string]any
			_ = json.Unmarshal(data, &value)
			return value
		}
		hello := read()
		if hello == nil {
			return
		}
		data, _ := json.Marshal(map[string]any{"protocol_version": 1, "request_id": hello["request_id"], "code": "ok", "role": "management"})
		rawFrame(t, conn, string(data))
		request := read()
		if request != nil {
			seen <- request["operation"].(string)
		}
	}()
	d := daemon(t, nil, infra.DaemonOptions{})
	epoch := strings.Repeat("e", 32)
	_, err = d.Launch(context.Background(), root, app.LaunchRequest{Epoch: epoch, Worktree: domain.WorktreeID("w_" + strings.Repeat("1", 64)), Profile: "codex", RequestID: "lost-ack"})
	if !errors.Is(err, app.ErrLaunchUnknown) {
		t.Fatalf("lost acknowledgement was falsely known: %v", err)
	}
	<-done
	select {
	case operation := <-seen:
		if operation != "launch" {
			t.Fatal("wrong named operation")
		}
	default:
		t.Fatal("request was not observed")
	}
}
