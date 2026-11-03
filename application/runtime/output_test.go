package runtime_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

func outputFixture(t *testing.T, limits app.OutputLimits) (*app.Output, app.OutputSelection) {
	t.Helper()
	owner, req, factory, _ := runtimeFixture(t, false)
	t.Cleanup(factory.child.finish)
	run, err := owner.Launch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	selection := app.OutputSelection{Identity: run.Identity, Worktree: run.Target.Worktree().ID(), Profile: run.Profile.ID()}
	output, err := app.NewOutput(selection, limits)
	if err != nil {
		t.Fatal(err)
	}
	return output, selection
}
func TestOutputSnapshotDetachReconnectSameIdentityAndRawBytes(t *testing.T) {
	output, selection := outputFixture(t, app.OutputLimits{RetainedBytes: 64})
	data := []byte("日本語🙂\x1b[31m\x1b]52;c;ZmFrZQ==\a")
	if err := output.Append(data); err != nil {
		t.Fatal(err)
	}
	sub, err := output.Subscribe(selection, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first, err := sub.Next(context.Background())
	if err != nil || first.Kind != "snapshot" || first.Info.Selection != selection {
		t.Fatal("missing immutable snapshot")
	}
	frame, err := sub.Next(context.Background())
	if err != nil || !bytes.Equal(frame.Data, data) || frame.End != domain.ByteOffset(len(data)) {
		t.Fatal("raw terminal bytes changed")
	}
	sub.Close()
	if err = output.Append([]byte("tail")); err != nil {
		t.Fatal(err)
	}
	again, err := output.Subscribe(selection, frame.End)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	_, _ = again.Next(context.Background())
	tail, err := again.Next(context.Background())
	if err != nil || string(tail.Data) != "tail" {
		t.Fatal("detach lost output or restarted cursor")
	}
	foreign := selection
	foreign.Identity.Generation = strings.Repeat("9", 32)
	if _, err = output.Subscribe(foreign, 0); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("old generation rebound: %v", err)
	}
	if _, err = output.Subscribe(selection, 1000); !errors.Is(err, domain.ErrOutputCursor) {
		t.Fatalf("future cursor guessed: %v", err)
	}
}
func TestOutputHistoryGapAndExitedReconnectAreExplicit(t *testing.T) {
	output, selection := outputFixture(t, app.OutputLimits{RetainedBytes: 8})
	if err := output.Append([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	output.Finish(app.CaptureClosed, false)
	sub, err := output.Subscribe(selection, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	_, _ = sub.Next(context.Background())
	gap, err := sub.Next(context.Background())
	if err != nil || gap.Kind != "gap" || gap.Gap == nil || gap.Gap.From != 0 || gap.Gap.To != 2 {
		t.Fatal("evicted history was hidden")
	}
	frame, err := sub.Next(context.Background())
	if err != nil || string(frame.Data) != "23456789" {
		t.Fatal("wrong retained bytes after gap")
	}
	end, err := sub.Next(context.Background())
	if err != nil || end.Kind != "end" || !end.Info.Ended {
		t.Fatal("ended output did not reconnect")
	}
}
func TestOutputSlowSubscriberAndCapacityDoNotBlockPublisher(t *testing.T) {
	output, selection := outputFixture(t, app.OutputLimits{PendingBytes: 8192})
	var readers []*app.OutputSubscription
	for range domain.MaxOutputSubscribers {
		s, err := output.Subscribe(selection, 0)
		if err != nil {
			t.Fatal(err)
		}
		readers = append(readers, s)
		defer s.Close()
	}
	if _, err := output.Subscribe(selection, 0); !errors.Is(err, domain.ErrLimit) {
		t.Fatalf("subscriber overflow: %v", err)
	}
	if err := output.Append(bytes.Repeat([]byte("x"), 8192)); err != nil {
		t.Fatal(err)
	}
	_, _ = readers[0].Next(context.Background())
	gap, err := readers[0].Next(context.Background())
	if err != nil || gap.Kind != "gap" || gap.Gap == nil || gap.Gap.Reason != "slow_consumer" || !gap.Gap.DeliveryUnknown {
		t.Fatal("slow detach silently claimed delivery")
	}
	healthy, err := output.Subscribe(selection, 8192)
	if err != nil {
		t.Fatalf("detached reader retained capacity: %v", err)
	}
	defer healthy.Close()
	_, _ = healthy.Next(context.Background())
	if err = output.Append([]byte("fresh")); err != nil {
		t.Fatal(err)
	}
	frame, err := healthy.Next(context.Background())
	if err != nil || string(frame.Data) != "fresh" {
		t.Fatal("slow reader blocked another subscriber")
	}
}
func TestOutputResizeEvidenceAndCanceledReaderDoNotCreateAuthority(t *testing.T) {
	output, selection := outputFixture(t, app.OutputLimits{})
	if err := output.AcknowledgeResize(selection, 0, 24, 80); err != nil {
		t.Fatal(err)
	}
	if err := output.AcknowledgeResize(selection, 1, 30, 100); err != nil {
		t.Fatal(err)
	}
	if err := output.AcknowledgeResize(selection, 1, 30, 100); err != nil {
		t.Fatal("exact duplicate ack not idempotent")
	}
	if err := output.AcknowledgeResize(selection, 1, 31, 100); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("conflicting duplicate resize fabricated ack")
	}
	if err := output.AcknowledgeResize(selection, 0, 24, 80); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("out-of-order resize rewound ack")
	}
	if err := output.Append([]byte("native text says resize applied 999")); err != nil {
		t.Fatal(err)
	}
	sub, err := output.Subscribe(selection, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	snapshot, _ := sub.Next(context.Background())
	if snapshot.Info.Resize.Sequence != 1 {
		t.Fatal("terminal text became resize authority")
	}
	_, _ = sub.Next(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err = sub.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("reader cancellation: %v", err)
	}
	if err = output.Append([]byte("owner still captures")); err != nil {
		t.Fatal("client cancel killed output producer")
	}
}

func TestOutputLazyHistoryWrapIsNotSlowLiveBacklog(t *testing.T) {
	output, selection := outputFixture(t, app.OutputLimits{RetainedBytes: 64, PendingBytes: 8192})
	if err := output.Append(bytes.Repeat([]byte("h"), 64)); err != nil {
		t.Fatal(err)
	}
	sub, err := output.Subscribe(selection, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	if err = output.Append([]byte("new")); err != nil {
		t.Fatal(err)
	}
	_, _ = sub.Next(context.Background())
	gap, err := sub.Next(context.Background())
	if err != nil || gap.Gap == nil || gap.Gap.Reason != "evicted" || gap.Gap.From != 0 || gap.Gap.To != 3 || gap.Gap.DeliveryUnknown {
		t.Fatal("history wrap mislabeled live overload")
	}
	frame, err := sub.Next(context.Background())
	if err != nil || len(frame.Data) != 64 {
		t.Fatal("history replay detached")
	}
	output.Finish(app.CaptureTailUnknown, true)
	gap, err = sub.Next(context.Background())
	if err != nil || gap.Gap == nil || gap.Gap.From != gap.Gap.To || !gap.Gap.DeliveryUnknown {
		t.Fatal("unknown tail lost")
	}
	end, err := sub.Next(context.Background())
	if err != nil || end.Kind != "end" {
		t.Fatal("missing capture end")
	}
}

func TestOutputRejectsMalformedWorktreeIdentity(t *testing.T) {
	_, selection := outputFixture(t, app.OutputLimits{})
	for _, worktree := range []string{strings.Repeat("a", 66), "w_" + strings.Repeat("G", 64), "w_" + strings.Repeat("A", 64)} {
		selection.Worktree = domain.WorktreeID(worktree)
		if _, err := app.NewOutput(selection, app.OutputLimits{}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("malformed worktree accepted")
		}
	}
}

func TestOutputOwnerResizeSequenceCannotJumpOrReuseForNewControl(t *testing.T) {
	output, selection := outputFixture(t, app.OutputLimits{})
	if err := output.AcknowledgeResize(selection, 0, 24, 80); err != nil {
		t.Fatal(err)
	}
	if err := output.AcknowledgeResize(selection, 5, 30, 100); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("owner acknowledgment skipped sequence")
	}
	if err := output.ValidateResize(selection, 1, 30, 100); err != nil {
		t.Fatal(err)
	}
	if err := output.AcknowledgeResize(selection, 1, 30, 100); err != nil {
		t.Fatal(err)
	}
	if err := output.ValidateResize(selection, 1, 30, 100); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("new request reused an applied sequence")
	}
	if err := output.AcknowledgeResize(selection, 1, 30, 100); err != nil {
		t.Fatal("exact existing acknowledgment lost idempotency")
	}
}
