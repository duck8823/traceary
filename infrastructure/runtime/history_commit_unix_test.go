//go:build darwin || linux

package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

// The fake observes only routing/context; it cannot read capture contents.
type historyHintClient struct {
	calls int
	root  string
	ctx   context.Context
}

type committedSQLiteObserver struct {
	path     string
	observed chan string
}

func (o committedSQLiteObserver) NotifyCommittedHistory(context.Context) {
	// This test observer deliberately performs an independent bounded read,
	// even if the optional caller has already returned. It owns and joins its
	// query and never borrows the writer transaction/connection/context.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reader := sqlite.NewEventDatasource(sqlite.NewDatabase(o.path, nil))
	rows, err := reader.ListRecentMetadata(ctx, apptypes.NewEventListCriteriaBuilder(2).Build())
	if err != nil || len(rows) != 1 {
		o.observed <- ""
		return
	}
	o.observed <- rows[0].EventID().String()
}

func TestHistoryCommitObserverSeesActualCommittedSQLiteThroughIndependentReader(t *testing.T) {
	f := historySQLite(t)
	observed := make(chan string, 1)
	u := usecase.NewEventUsecase(f.events, f.events, usecase.WithEventCommitObserver(committedSQLiteObserver{f.path, observed}))
	result, err := u.Log(context.Background(), "synthetic commit", types.EventKindNote, "cli", "fixture", "session", types.Workspace(f.repo), apptypes.LogRedaction{})
	if err != nil || !result.Inserted() {
		t.Fatal("primary capture failed", err)
	}
	select {
	case id := <-observed:
		if id != result.EventID().String() {
			t.Fatal("observer did not see independently committed canonical row")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("independent post-commit observer failed to terminate")
	}
}

func (c *historyHintClient) HintHistory(ctx context.Context, root string) error {
	c.calls++
	c.root = root
	c.ctx = ctx
	return errors.New("optional daemon unavailable")
}

func TestHistoryCommitObserverUsesOnlyExplicitPrivateRootAndOriginalContext(t *testing.T) {
	root := daemonRoot(t)
	client := &historyHintClient{}
	observer := infra.NewHistoryCommitObserver(client, root)
	if observer == nil {
		t.Fatal("valid explicit routing disabled")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observer.NotifyCommittedHistory(ctx)
	if client.calls != 1 || client.root != root || client.ctx != ctx {
		t.Fatal("observer changed routing/context or retried")
	}
	link := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	for _, invalid := range []string{"", "relative", root + "/missing", link, root + "/../" + filepath.Base(root)} {
		if infra.NewHistoryCommitObserver(client, invalid) != nil {
			t.Fatal("invalid root enabled optional hint")
		}
	}
	if infra.NewHistoryCommitObserver(nil, root) != nil {
		t.Fatal("nil client enabled optional hint")
	}
	if client.calls != 1 {
		t.Fatal("constructing observer sent data or started daemon")
	}
}

func TestPTYHistoryRootIsTrustedStoreBoundRoutingNotProfileProbeEnvironment(t *testing.T) {
	_, descriptor, result := ptyFixture(t)
	root := daemonRoot(t)
	registry, _ := service(t, filepath.Dir(result))
	versionResult := filepath.Join(root, "version-root")
	env := []string{"TRACEARY_PTY_FIXTURE=1", "FIXTURE_RESULT=" + result,
		"FIXTURE_VERSION_ENV_RESULT=" + versionResult, "GORACE=atexit_sleep_ms=0",
		"TRACEARY_RUNTIME_DIR=/untrusted/inherited", "TRACEARY_RUNTIME_OTHER=untrusted"}
	launcher, err := infra.NewPTYLauncherWithHistoryRoot(registry, env, root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := descriptor.Snapshot().Profile.Snapshot()
	if _, err = launcher.InspectProfile(context.Background(), snapshot.ID, snapshot.Host, snapshot.Executable, snapshot.Version); err != nil {
		t.Fatal(err)
	}
	probe, err := os.ReadFile(versionResult)
	if err != nil || len(probe) != 0 {
		t.Fatal("version inspection inherited runtime routing", err)
	}
	child, err := launcher.Start(context.Background(), descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if code, err := child.Wait(); err != nil || code != 7 {
		t.Fatal("finite owned child failed", code, err)
	}
	data, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ RuntimeRoot string }
	if err = json.Unmarshal(data, &got); err != nil || got.RuntimeRoot != root {
		t.Fatal("child did not receive trusted routing", err)
	}
	if _, err = infra.NewPTYLauncherWithHistoryRoot(registry, env, root+"/missing"); err == nil {
		t.Fatal("missing root accepted")
	}
}
