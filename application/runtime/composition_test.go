package runtime_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

func TestRuntimeIdleBarrierPreservesOwnedChildAndRejectsNewLaunch(t *testing.T) {
	owner, req, factory, _ := runtimeFixture(t, false)
	launched, err := owner.Launch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	owner.DisableLaunches()
	newReq := req
	newReq.RequestID = "not-admitted"
	if _, err = owner.Launch(context.Background(), newReq); !errors.Is(err, app.ErrRuntimeBusy) {
		t.Fatalf("disabled owner admitted launch: %v", err)
	}
	if _, err = owner.Launch(context.Background(), req); err != nil {
		t.Fatalf("disabled owner hid receipt: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err = owner.AwaitIdle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("live child reported idle: %v", err)
	}
	if len(owner.List()) != 1 || !owner.Disabled() || launched.State != domain.RunLive || factory.calls.Load() != 1 {
		t.Fatal("shutdown intent lost live ownership")
	}
}
func TestRuntimePagesAreBoundedAndInvalidateMixedRevision(t *testing.T) {
	owner, req, _, _ := runtimeFixture(t, false)
	for _, id := range []string{"one", "two", "three"} {
		req.RequestID = id
		if _, err := owner.Launch(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	page, err := owner.Page(app.RunPageRequest{Epoch: req.Epoch})
	if err != nil || len(page.Runs) != 2 || page.Next == "" || page.Revision == 0 {
		t.Fatalf("bad bounded page: %v", err)
	}
	last, err := owner.Page(app.RunPageRequest{Epoch: req.Epoch, Cursor: page.Next, Revision: page.Revision})
	if err != nil || len(last.Runs) != 1 || last.Next != "" {
		t.Fatalf("bad continuation: %v", err)
	}
	req.RequestID = "four"
	if _, err = owner.Launch(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Page(app.RunPageRequest{Epoch: req.Epoch, Cursor: page.Next, Revision: page.Revision}); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("mixed page silently accepted: %v", err)
	}
}

func TestRuntimeRestartNeverAdoptsReusedDiagnosticPIDOrOldGeneration(t *testing.T) {
	old, oldRequest, oldFactory, _ := runtimeFixtureEpoch(t, false, strings.Repeat("a", 32))
	oldRun, err := old.Launch(context.Background(), oldRequest)
	if err != nil {
		t.Fatal(err)
	}
	oldFactory.child.finish()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = old.AwaitExit(ctx, oldRun.Identity); err != nil {
		t.Fatal(err)
	}
	current, currentRequest, currentFactory, _ := runtimeFixtureEpoch(t, false, strings.Repeat("b", 32))
	currentRun, err := current.Launch(context.Background(), currentRequest)
	if err != nil {
		t.Fatal(err)
	}
	if currentRun.PID != oldRun.PID || currentRun.Identity == oldRun.Identity {
		t.Fatal("fixture did not model PID reuse with fresh identity")
	}
	if _, err = current.AwaitExit(context.Background(), oldRun.Identity); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("saved PID/old generation adopted: %v", err)
	}
	if _, err = current.Launch(context.Background(), oldRequest); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("old epoch receipt replayed: %v", err)
	}
	list := current.List()
	if len(list) != 1 || list[0].State != domain.RunLive || list[0].Identity != currentRun.Identity || currentFactory.calls.Load() != 1 {
		t.Fatal("old exit affected replacement owner")
	}
}
