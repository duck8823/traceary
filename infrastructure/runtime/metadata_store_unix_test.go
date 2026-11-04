//go:build darwin || linux

package runtime_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"golang.org/x/sys/unix"
)

func metadataAuditFixture(t *testing.T) (*infra.MetadataStore, *infra.ExecutionStore, string) {
	t.Helper()
	_, descriptor, _ := ptyFixture(t)
	root := daemonRoot(t)
	s, err := infra.NewExecutionStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snapshot := descriptor.Snapshot()
	metadataRegistry, err := infra.OpenRegistryStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := app.NewRegistry(metadataRegistry, infra.RegistryResolver{})
	if err != nil {
		t.Fatal(err)
	}
	project, err := registry.RegisterProject(ctx, snapshot.Target.Worktree().Path())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.RegisterWorktree(ctx, project.ID(), snapshot.Target.Worktree().Path(), snapshot.Target.Worktree().Store()); err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterProfile(ctx, snapshot.Profile); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveLaunch(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	selection := app.OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}
	receipt, err := domain.ControlReceiptFrom(domain.ControlSnapshot{Identity: snapshot.Identity, Intent: domain.ControlIntentState{RequestID: "fixture-manual", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 5}, Outcome: domain.ControlPending})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveControl(ctx, selection, receipt); err != nil {
		t.Fatal(err)
	}
	metadata, err := infra.OpenMetadataStore(root, infra.MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	return metadata, s, root
}

func TestMetadataAuditExportPreviewAndConfirmedForgetPreservePolicies(t *testing.T) {
	m, execution, root := metadataAuditFixture(t)
	ctx := context.Background()
	registryBefore, err := os.ReadFile(filepath.Join(root, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	originalRuns, err := execution.ListLaunches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	corePath := originalRuns[0].Target.Worktree().Store()
	coreBefore, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := m.Export(ctx)
	if err != nil || exported.Version != 1 || exported.Authority != "persisted_metadata_not_liveness" || len(exported.Runs) != 1 || len(exported.Profiles) != 1 || exported.TotalReceipts != 1 || exported.UncertainRuns != 1 || exported.UncertainReceipts != 1 {
		t.Fatal("invalid safe audit summary", err)
	}
	encoded, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"pid"`, `"executable"`, `"payload"`, `"environment"`, `"store"`, `"path"`, `"input"`} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatal("unsafe audit contents")
		}
	}
	before, err := os.ReadFile(filepath.Join(root, "execution.json"))
	if err != nil {
		t.Fatal(err)
	}
	preview, err := m.PreviewForget(ctx)
	if err != nil || preview.Snapshot.Digest != exported.Digest || !preview.RecoveryRecordLoss || !preview.OfflineRequired {
		t.Fatal("preview lost exact scope/warnings", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "execution.json"))
	if string(before) != string(after) {
		t.Fatal("preview changed history")
	}
	confirmation, err := domain.MetadataForgetConfirmationOf(preview.Snapshot.Digest, true, true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.Forget(ctx, confirmation)
	if err != nil || result.Runs != 1 || result.Receipts != 1 || result.Outcome != "forgotten" {
		t.Fatal("confirmed offline forget failed", err)
	}
	runs, err := execution.ListLaunches(ctx)
	if err != nil || len(runs) != 0 {
		t.Fatal("launch history remained", err)
	}
	profiles, err := execution.ListProfiles(ctx)
	if err != nil || len(profiles) != 1 {
		t.Fatal("profile policies erased", err)
	}
	if _, err = m.Forget(ctx, confirmation); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("stale preview accepted", err)
	}
	if _, err = m.Export(ctx); err != nil {
		t.Fatal("forget left invalid receipt/execution references", err)
	}
	registryAfter, _ := os.ReadFile(filepath.Join(root, "registry.json"))
	coreAfter, _ := os.ReadFile(corePath)
	if !bytes.Equal(registryBefore, registryAfter) || !bytes.Equal(coreBefore, coreAfter) {
		t.Fatal("forget modified Registry or core history")
	}
}

func TestMetadataForgetPartialAndUncertainPrefixesRemainInspectableWithoutReplay(t *testing.T) {
	for _, failure := range []struct {
		name           string
		at             int
		after          bool
		receipts, runs int
		uncertain      bool
	}{
		{"before-first", 1, false, 1, 1, false}, {"uncertain-first", 1, true, 0, 1, true},
		{"before-second", 2, false, 0, 1, true}, {"uncertain-second", 2, true, 0, 0, true},
	} {
		t.Run(failure.name, func(t *testing.T) {
			m, _, root := metadataAuditFixture(t)
			ctx := context.Background()
			p, err := m.PreviewForget(ctx)
			if err != nil {
				t.Fatal(err)
			}
			c, err := domain.MetadataForgetConfirmationOf(p.Snapshot.Digest, true, true)
			if err != nil {
				t.Fatal(err)
			}
			infra.FailMetadataCommitForTest(m, failure.at, failure.after)
			result, err := m.Forget(ctx, c)
			if err == nil || result.Outcome != "" || errors.Is(err, app.ErrMetadataForgetUncertain) != failure.uncertain {
				t.Fatal("write failure became success or lost partial uncertainty", err)
			}
			// A NEW service/export must accept every committed prefix. It cannot
			// rely on the injected object's in-memory state or redo receipt writes.
			fresh, err := infra.OpenMetadataStore(root, infra.MaxRegistryBytes)
			if err != nil {
				t.Fatal(err)
			}
			view, err := fresh.Export(ctx)
			if err != nil || len(view.Runs) != failure.runs || view.TotalReceipts != failure.receipts {
				t.Fatal("invalid partial reference state", err)
			}
			before, _ := os.ReadFile(filepath.Join(root, "receipts.json"))
			next, err := fresh.PreviewForget(ctx)
			if err != nil {
				t.Fatal(err)
			}
			confirm, err := domain.MetadataForgetConfirmationOf(next.Snapshot.Digest, true, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = fresh.Forget(ctx, confirm); err != nil {
				t.Fatal("fresh explicit decision could not finish", err)
			}
			after, _ := os.ReadFile(filepath.Join(root, "receipts.json"))
			if failure.receipts == 0 && !bytes.Equal(before, after) {
				t.Fatal("fresh preview recovery redundantly rewrote receipts")
			}
		})
	}
}

func TestMetadataForgetNoOpCancellationRootBindingAndSchemaRefusals(t *testing.T) {
	t.Run("empty-noop", func(t *testing.T) {
		root := daemonRoot(t)
		m, err := infra.OpenMetadataStore(root, infra.MaxRegistryBytes)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		p, err := m.PreviewForget(ctx)
		if err != nil {
			t.Fatal(err)
		}
		c, err := domain.MetadataForgetConfirmationOf(p.Snapshot.Digest, true, true)
		if err != nil {
			t.Fatal(err)
		}
		result, err := m.Forget(ctx, c)
		if err != nil || result.Outcome != "unchanged" {
			t.Fatal("empty no-op failed", err)
		}
		for _, name := range []string{"registry.json", "execution.json", "receipts.json"} {
			if _, err = os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
				t.Fatal("no-op created an envelope")
			}
		}
	})
	t.Run("cancel-and-root-binding", func(t *testing.T) {
		m, _, root := metadataAuditFixture(t)
		ctx := context.Background()
		p, err := m.PreviewForget(ctx)
		if err != nil {
			t.Fatal(err)
		}
		c, err := domain.MetadataForgetConfirmationOf(p.Snapshot.Digest, true, true)
		if err != nil {
			t.Fatal(err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err = m.Forget(cancelled, c); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled mutation accepted", err)
		}
		otherRoot := daemonRoot(t)
		for _, name := range []string{"execution.json", "receipts.json"} {
			data, e := os.ReadFile(filepath.Join(root, name))
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(otherRoot, name), data, 0600); e != nil {
				t.Fatal(e)
			}
		}
		other, err := infra.OpenMetadataStore(otherRoot, infra.MaxRegistryBytes)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = other.Forget(ctx, c); !errors.Is(err, domain.ErrChanged) {
			t.Fatal("identical bytes in another physical root accepted old preview", err)
		}
		view, err := m.Export(ctx)
		if err != nil || view.Digest != p.Snapshot.Digest {
			t.Fatal("refusal changed history", err)
		}
	})
	for _, kind := range []string{"future", "malformed", "unsafe", "symlink", "overflow", "known-lifecycle-intermediate"} {
		t.Run(kind, func(t *testing.T) {
			m, _, root := metadataAuditFixture(t)
			ctx := context.Background()
			p, err := m.PreviewForget(ctx)
			if err != nil {
				t.Fatal(err)
			}
			c, err := domain.MetadataForgetConfirmationOf(p.Snapshot.Digest, true, true)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "execution.json")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "future":
				err = os.WriteFile(path, []byte(`{"version":2}`), 0600)
			case "malformed":
				err = os.WriteFile(path, []byte(`{"version":1`), 0600)
			case "unsafe":
				err = os.Chmod(path, 0644)
			case "symlink":
				saved := filepath.Join(t.TempDir(), "saved")
				err = os.Rename(path, saved)
				if err == nil {
					err = os.Symlink(saved, path)
				}
			case "overflow":
				data := strings.Replace(string(original), `"revision":2`, `"revision":18446744073709551615`, 1)
				if data == string(original) {
					t.Fatal("fixture revision not2")
				}
				err = os.WriteFile(path, []byte(data), 0600)
			case "known-lifecycle-intermediate":
				err = os.WriteFile(filepath.Join(root, "lifecycle.json"), []byte(`{"version":1}`), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind == "overflow" {
				p, err = m.PreviewForget(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if p.Snapshot.ExecutionRevision != "18446744073709551615" {
					t.Fatal("preview lost uint64 revision precision")
				}
				c, err = domain.MetadataForgetConfirmationOf(p.Snapshot.Digest, true, true)
				if err != nil {
					t.Fatal(err)
				}
			}
			receipts, _ := os.ReadFile(filepath.Join(root, "receipts.json"))
			if _, err = m.Forget(ctx, c); err == nil {
				t.Fatal("unsafe/incompatible/overflow state accepted")
			}
			after, _ := os.ReadFile(filepath.Join(root, "receipts.json"))
			if !bytes.Equal(receipts, after) {
				t.Fatal("preflight failure partially erased receipts")
			}
		})
	}
}

func TestMetadataAuditBoundsAndNoFollowFailuresNeverCreateOrEraseHistory(t *testing.T) {
	for _, kind := range []string{"missing-root", "root-alias", "budget", "oversized-receipts", "fifo-receipts"} {
		t.Run(kind, func(t *testing.T) {
			m, _, root := metadataAuditFixture(t)
			ctx := context.Background()
			before, err := os.ReadFile(filepath.Join(root, "execution.json"))
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing-root":
				missing := filepath.Join(root, "missing")
				if _, err = infra.OpenMetadataStore(missing, infra.MaxRegistryBytes); err == nil {
					t.Fatal("missing root created")
				}
				if _, err = os.Stat(missing); !os.IsNotExist(err) {
					t.Fatal("open created missing root")
				}
				return
			case "root-alias":
				link := filepath.Join(t.TempDir(), "alias")
				if err = os.Symlink(root, link); err != nil {
					t.Fatal(err)
				}
				if _, err = infra.OpenMetadataStore(link, infra.MaxRegistryBytes); err == nil {
					t.Fatal("symlink root accepted")
				}
				return
			case "budget":
				m, err = infra.OpenMetadataStore(root, 64)
				if err != nil {
					t.Fatal(err)
				}
			case "oversized-receipts":
				err = os.Truncate(filepath.Join(root, "receipts.json"), 64*1024*1024+1)
				if err != nil {
					t.Fatal(err)
				}
			case "fifo-receipts":
				path := filepath.Join(root, "receipts.json")
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err = unix.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err = m.Export(ctx)
			expected := domain.ErrLimit
			if kind == "fifo-receipts" {
				expected = domain.ErrUnsafe
			}
			if !errors.Is(err, expected) {
				t.Fatal("unsafe/over-budget audit did not fail explicitly", err)
			}
			after, e := os.ReadFile(filepath.Join(root, "execution.json"))
			if e != nil || !bytes.Equal(before, after) {
				t.Fatal("failed audit changed history", e)
			}
		})
	}
}

func TestMetadataForgetRefusesActiveOwnerAndUnknownSchemaWithoutChangingHistory(t *testing.T) {
	m, _, root := metadataAuditFixture(t)
	ctx := context.Background()
	p, err := m.PreviewForget(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := domain.MetadataForgetConfirmationOf(p.Snapshot.Digest, true, true)
	if err != nil {
		t.Fatal(err)
	}
	d := daemon(t, nil, infra.DaemonOptions{})
	_, _ = startDaemon(t, d, root)
	if _, err = m.Forget(ctx, c); !errors.Is(err, app.ErrRuntimeBusy) {
		t.Fatal("active owner not refused", err)
	}
	if err = os.WriteFile(filepath.Join(root, "future.json"), []byte(`{"version":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Export(ctx); !errors.Is(err, domain.ErrUnsupported) {
		t.Fatal("unknown metadata silently omitted", err)
	}
}

func TestMetadataForgetChecksActualLeaseAtCommitAndCancellationAfterFirstEffect(t *testing.T) {
	for _, kind := range []string{"lease-replaced", "cancel-after-first"} {
		t.Run(kind, func(t *testing.T) {
			m, _, root := metadataAuditFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			preview, err := m.PreviewForget(ctx)
			if err != nil {
				t.Fatal(err)
			}
			confirmation, err := domain.MetadataForgetConfirmationOf(preview.Snapshot.Digest, true, true)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "lease-replaced" {
				infra.ReplaceMetadataLeaseAtCommitForTest(m)
			} else {
				infra.CancelMetadataAfterFirstCommitForTest(m, cancel)
			}
			_, err = m.Forget(ctx, confirmation)
			if kind == "lease-replaced" && !errors.Is(err, domain.ErrChanged) {
				t.Fatal("actual pre-rename lease replacement ignored", err)
			}
			if kind == "cancel-after-first" && (!errors.Is(err, app.ErrMetadataForgetUncertain) || !errors.Is(err, context.Canceled)) {
				t.Fatal("post-effect cancellation promised rollback/success", err)
			}
			fresh, err := infra.OpenMetadataStore(root, infra.MaxRegistryBytes)
			if err != nil {
				t.Fatal(err)
			}
			view, err := fresh.Export(context.Background())
			if err != nil || len(view.Runs) != 1 {
				t.Fatal("mutation left invalid execution", err)
			}
			expected := 1
			if kind == "cancel-after-first" {
				expected = 0
			}
			if view.TotalReceipts != expected {
				t.Fatal("pre/post effect classification wrong")
			}
		})
	}
}

func TestMetadataForgetRejectsEquivalentRawContentChangesBeforeAnyEffect(t *testing.T) {
	m, _, root := metadataAuditFixture(t)
	ctx := context.Background()
	preview, err := m.PreviewForget(ctx)
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := domain.MetadataForgetConfirmationOf(preview.Snapshot.Digest, true, true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "execution.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pretty bytes.Buffer
	if err = json.Indent(&pretty, data, "", "  "); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, pretty.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Forget(ctx, confirmation); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("raw-content stale preview accepted despite unchanged revision", err)
	}
	view, err := m.Export(ctx)
	if err != nil || len(view.Runs) != 1 || view.TotalReceipts != 1 {
		t.Fatal("stale preview changed history", err)
	}
}

// TestOwnedMetadataLeaseProcessHelper is a finite explicitly selected lock holder,
// not a daemon/native AI, and never interprets saved process ownership records.
func TestOwnedMetadataLeaseProcessHelper(t *testing.T) {
	if os.Getenv("TRACEARY_METADATA_LOCK_FIXTURE") != "1" {
		t.Skip("subprocess-only fixture")
	}
	fd, err := unix.Open(filepath.Join(os.Getenv("TRACEARY_METADATA_LOCK_ROOT"), "daemon.lock"), unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(fd) }()
	if err = unix.Flock(fd, unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stdout.WriteString("locked\n"); err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadFull(os.Stdin, make([]byte, 1))
}

func TestMetadataForgetRefusesSeparateProcessActualDaemonLifetimeLease(t *testing.T) {
	m, _, root := metadataAuditFixture(t)
	ctx := context.Background()
	p, err := m.PreviewForget(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := domain.MetadataForgetConfirmationOf(p.Snapshot.Digest, true, true)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	childCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	child := exec.CommandContext(childCtx, executable, "-test.run=^TestOwnedMetadataLeaseProcessHelper$")
	child.Env = append(os.Environ(), "TRACEARY_PTY_FIXTURE=0", "TRACEARY_METADATA_LOCK_FIXTURE=1", "TRACEARY_METADATA_LOCK_ROOT="+root, "GORACE=atexit_sleep_ms=0")
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	defer func() {
		_ = stdin.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("owned lease helper did not exit cleanly: %v", err)
			}
		case <-time.After(3 * time.Second):
			cancel()
			<-done
			t.Error("owned lease helper needed timeout cleanup")
		}
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatal("owned lease helper not ready", err)
	}
	if _, err = m.Forget(ctx, c); !errors.Is(err, app.ErrRuntimeBusy) {
		t.Fatal("separate-process OS lease was not authoritative", err)
	}
	view, err := m.Export(ctx)
	if err != nil || view.Digest != p.Snapshot.Digest {
		t.Fatal("busy refusal altered history", err)
	}
}

func TestMetadataAuditRetainsPendingReceiptUncertaintyAfterRecordedExit(t *testing.T) {
	m, execution, _ := metadataAuditFixture(t)
	ctx := context.Background()
	runs, err := execution.ListLaunches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run := runs[0]
	d, err := domain.LaunchDescriptorOf(run.Identity, run.Target, run.Profile)
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic diagnostic metadata only; this PID is never probed/signalled.
	if err = d.Started(4242); err != nil {
		t.Fatal(err)
	}
	if err = d.Exit(0); err != nil {
		t.Fatal(err)
	}
	if err = execution.SaveLaunch(ctx, d.Snapshot()); err != nil {
		t.Fatal(err)
	}
	view, err := m.Export(ctx)
	if err != nil || view.Runs[0].State != domain.RunExited || !view.Runs[0].Uncertain || view.UncertainReceipts != 1 || view.Runs[0].UncertainReceipts != 1 {
		t.Fatal("recorded exit incorrectly removed unknown delivery uncertainty", err)
	}
}
