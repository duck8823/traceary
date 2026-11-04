package runtime_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

func TestObservationProjectionPreservesOriginalIntakeAndAtomicallyWakesSnapshot(t *testing.T) {
	owner, req, _, _ := runtimeFixture(t, false)
	run, err := owner.Launch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	o := selectedObservation(t, run, "original")
	view, err := owner.SubscribeObservations(o.Selection)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	receipt, err := owner.AdmitObservation(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now()
	if err = owner.ConsumeObservation(context.Background(), receipt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-view.Changed:
	default:
		t.Fatal("accepted observation did not wake subscriber")
	}
	view, err = owner.SubscribeObservations(o.Selection)
	if err != nil {
		t.Fatal(err)
	}
	p := view.Projection
	if !p.HasLastEvent || p.LastEvent.EventID != "original" || p.LastEvent.Received.Wall.Before(before) || p.LastEvent.Received.Wall.After(after) {
		t.Fatal("original receipt replaced at consumption", p)
	}
	if p.LastEvent.SourceVersion != run.Profile.Snapshot().Version || p.LastEvent.Boundary != domain.ObservationPromptBoundary || p.Interaction != domain.ProjectionUnknown || p.Attention.EventID != "" || p.Process != domain.RunLive {
		t.Fatal(p)
	}
	select {
	case <-view.Changed:
		t.Fatal("snapshot returned already-stale change channel")
	default:
	}
	other, _, _, _ := runtimeFixture(t, false)
	if err = other.ConsumeObservation(context.Background(), receipt); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("foreign owner accepted receipt", err)
	}
	if err = owner.ConsumeObservation(context.Background(), app.ObservationReceipt{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("zero receipt accepted", err)
	}
}

func TestObservationProjectionDoesNotBreakLaunchPageContinuationOrLongUnboundCapture(t *testing.T) {
	owner, req, _, _ := runtimeFixture(t, false)
	var run domain.RunSnapshot
	for i := 0; i < 3; i++ {
		req.RequestID = "page:" + strconv.Itoa(i)
		var err error
		run, err = owner.Launch(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := owner.Page(app.RunPageRequest{Epoch: req.Epoch})
	if err != nil || page.Next == "" {
		t.Fatal(page, err)
	}
	o := selectedObservation(t, run, "start")
	for i := 0; i < domain.MaxProjectionEvents*2; i++ {
		o = selectedObservation(t, run, "unbound:"+strconv.Itoa(i))
		if err = owner.RecordObservation(context.Background(), o); err != nil {
			t.Fatalf("capture stopped at %d: %v", i, err)
		}
	}
	if _, err = owner.Page(app.RunPageRequest{Epoch: page.Epoch, Revision: page.Revision, Cursor: page.Next}); err != nil {
		t.Fatal("telemetry invalidated launch page", err)
	}
	last, present, err := owner.LastObservation(o.Selection)
	if err != nil || !present || last != o.Metadata {
		t.Fatal("ordinary capture stopped", present, err)
	}
	view, err := owner.SubscribeObservations(o.Selection)
	if err != nil || view.Projection.Exhausted || view.Projection.LastEvent.EventID != "unbound:511" {
		t.Fatal(view, err)
	}
}

func TestObservationProjectionUncertaintyRevokesButRetainsHistoricalMetadata(t *testing.T) {
	owner, req, factory, _ := runtimeFixture(t, false)
	run, err := owner.Launch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	o := selectedObservation(t, run, "historical")
	if err = owner.RecordObservation(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	before, err := owner.SubscribeObservations(o.Selection)
	if err != nil {
		t.Fatal(err)
	}
	// Publication succeeded; only the subsequent actual-exit journal save fails.
	factory.journal.fail = true
	factory.child.finish()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err = owner.AwaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := owner.SubscribeObservations(o.Selection)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Run.PublicationUncertain || after.Projection.ContinuityRevision <= before.Projection.ContinuityRevision || after.Projection.Interaction != domain.ProjectionUnknown || !after.Projection.HasLastEvent {
		t.Fatal(after)
	}
	if _, err = owner.AdmitObservation(context.Background(), o); !errors.Is(err, app.ErrRuntimeRejected) {
		t.Fatal("uncertain ownership admitted", err)
	}
	if _, present, err := owner.LastObservation(o.Selection); err != nil || present {
		t.Fatal("live sample retained after uncertainty", present, err)
	}
}
