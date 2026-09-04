package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func TestTwoTierSearch_MergedOrderDedupeThenSinglePage(t *testing.T) {
	t.Parallel()
	fx := newTwoTierFixture(t)
	whole := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	frac := time.Date(2026, 8, 1, 10, 0, 0, 500_000_000, time.UTC)

	fx.seedSession(t, "sess-frac", "ws", "cli", "codex", whole)
	fx.seedEvent(t, "evt-frac", "sess-frac", "ws", "cli", "codex", "order-term in fractional event", types.EventKindNote, frac)

	fx.seedSession(t, "sess-whole", "ws", "cli", "codex", whole)
	fx.seedEvent(t, "evt-whole", "sess-whole", "ws", "cli", "codex", "order-term in whole-second event", types.EventKindNote, whole)

	fx.seedSession(t, "sess-tie", "ws", "cli", "codex", whole)
	fx.seedEvent(t, "evt-tie-a", "sess-tie", "ws", "cli", "codex", "order-term tie a", types.EventKindNote, whole)
	fx.seedEvent(t, "evt-tie-z", "sess-tie", "ws", "cli", "codex", "order-term tie z", types.EventKindNote, whole)

	fx.seedSession(t, "sess-both", "ws", "cli", "codex", whole)
	fx.seedEvent(t, "evt-both", "sess-both", "ws", "cli", "codex", "order-term in body and summary", types.EventKindNote, whole)
	fx.seedRefinement(t, "sess-both", "order-term lives in the refinement too", frac, "evt-both")

	full := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(50).Query("order-term").Build())
	if twoTierSessionTier(full, "sess-both") != apptypes.SearchHitTierRefinement {
		t.Fatalf("sess-both tier = %q, want refinement (fallback session suppressed)", twoTierSessionTier(full, "sess-both"))
	}
	for _, hit := range full.Sessions() {
		if hit.SessionID().String() == "sess-both" && hit.Tier() == apptypes.SearchHitTierFallback {
			t.Fatal("sess-both still has a fallback session row")
		}
	}
	if !containsString(twoTierEventIDs(full), "evt-both") {
		t.Fatalf("matching event in a refinement-hit session was dropped: %v", twoTierEventIDs(full))
	}

	// Merged total order for this fixture (time desc, then tier/kind, then ids):
	// 1 sess-both refinement (produced_at = frac)
	// 2 evt-frac (created_at = frac)
	// 3 evt-both, 4 evt-tie-a, 5 evt-tie-z, 6 evt-whole (all created_at = whole)
	// then fallback sessions at started_at = whole.
	page1 := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(3).Query("order-term").Offset(0).Build())
	page2 := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(3).Query("order-term").Offset(3).Build())
	if diff := cmp.Diff([]string{"evt-frac", "evt-both"}, twoTierEventIDs(page1)); diff != "" {
		t.Fatalf("page1 events mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"sess-both"}, twoTierSessionIDs(page1)); diff != "" {
		t.Fatalf("page1 sessions mismatch (-want +got):\n%s", diff)
	}
	if page1.Sessions()[0].Tier() != apptypes.SearchHitTierRefinement {
		t.Fatalf("page1 session tier = %q", page1.Sessions()[0].Tier())
	}
	if diff := cmp.Diff([]string{"evt-tie-a", "evt-tie-z", "evt-whole"}, twoTierEventIDs(page2)); diff != "" {
		t.Fatalf("page2 events mismatch (-want +got):\n%s", diff)
	}
	if len(page2.Sessions()) != 0 {
		t.Fatalf("page2 sessions = %v, want empty (those merged rows are events)", twoTierSessionIDs(page2))
	}

	eventOrder := twoTierEventIDs(full)
	if indexOf(eventOrder, "evt-frac") > indexOf(eventOrder, "evt-whole") {
		t.Fatalf("fractional event must precede whole-second event: %v", eventOrder)
	}
	if indexOf(eventOrder, "evt-tie-a") > indexOf(eventOrder, "evt-tie-z") {
		t.Fatalf("binary id order failed: %v", eventOrder)
	}
}

func TestTwoTierSearch_KindAndFailuresStateRefinementApplicability(t *testing.T) {
	t.Parallel()
	fx := newTwoTierFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	fx.seedSession(t, "sess-disp", "ws", "cli", "codex", base)
	fx.seedEvent(t, "evt-note", "sess-disp", "ws", "cli", "codex", "disp-term in a note", types.EventKindNote, base.Add(time.Second))
	fx.seedAuditEvent(t, "evt-fail", "sess-disp", "ws", "cli", "codex", "false", "disp-term failed output", true, base.Add(2*time.Second))
	fx.seedAuditEvent(t, "evt-ok", "sess-disp", "ws", "cli", "codex", "true", "disp-term ok output", false, base.Add(3*time.Second))
	fx.seedRefinement(t, "sess-disp", "disp-term in the refinement", base.Add(4*time.Second), "evt-ok")

	t.Run("kind", func(t *testing.T) {
		kindPage := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).
			Query("disp-term").Kind(types.EventKindNote).Build())
		if kindPage.RefinementDisposition() != apptypes.RefinementDispositionKindExcluded {
			t.Fatalf("kind disposition = %q", kindPage.RefinementDisposition())
		}
		if kindPage.RefinementMatchCount() == 0 {
			t.Fatal("kind exclusion must still observe the matching refinement")
		}
		if len(kindPage.Sessions()) != 0 {
			t.Fatalf("kind must omit session rows, got %v", twoTierSessionIDs(kindPage))
		}
		if diff := cmp.Diff([]string{"evt-note"}, twoTierEventIDs(kindPage)); diff != "" {
			t.Fatalf("kind events mismatch (-want +got):\n%s", diff)
		}
	})
	t.Run("failures", func(t *testing.T) {
		failPage := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).
			Query("disp-term").FailuresOnly(true).Build())
		if failPage.RefinementDisposition() != apptypes.RefinementDispositionFailuresExcluded {
			t.Fatalf("failures disposition = %q", failPage.RefinementDisposition())
		}
		if len(failPage.Sessions()) != 0 {
			t.Fatalf("failures must omit session rows, got %v", twoTierSessionIDs(failPage))
		}
		if diff := cmp.Diff([]string{"evt-fail"}, twoTierEventIDs(failPage)); diff != "" {
			t.Fatalf("failures events mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestTwoTierSearch_FilterEscapeFoldContract(t *testing.T) {
	t.Parallel()
	fx := newTwoTierFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	fx.seedSession(t, "sess-a", "ws-a", "cli", "codex", base)
	fx.seedEvent(t, "evt-a", "sess-a", "ws-a", "cli", "codex", "filter-term NEEDLE 100% keep_days", types.EventKindNote, base.Add(time.Minute))
	fx.seedRefinement(t, "sess-a", "filter-term NEEDLE 100% keep_days", base.Add(2*time.Minute), "evt-a")
	fx.seedSession(t, "sess-b", "ws-b", "gemini", "kimi", base.Add(3*time.Hour))
	fx.seedEvent(t, "evt-b", "sess-b", "ws-b", "gemini", "kimi", "filter-term other workspace", types.EventKindTranscript, base.Add(4*time.Hour))
	fx.seedRefinement(t, "sess-b", "filter-term other workspace", base.Add(5*time.Hour), "evt-b")

	t.Run("workspace", func(t *testing.T) {
		page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).
			Query("filter-term").Workspace(types.Workspace("ws-a")).Build())
		if containsString(twoTierSessionIDs(page), "sess-b") || containsString(twoTierEventIDs(page), "evt-b") {
			t.Fatalf("workspace filter leaked sess-b: events=%v sessions=%v", twoTierEventIDs(page), twoTierSessionIDs(page))
		}
	})
	t.Run("session", func(t *testing.T) {
		page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).
			Query("filter-term").SessionID(types.SessionID("sess-a")).Build())
		if containsString(twoTierSessionIDs(page), "sess-b") {
			t.Fatal("session filter leaked sess-b")
		}
	})
	t.Run("client", func(t *testing.T) {
		page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).
			Query("filter-term").Client(types.Client("cli")).Build())
		if containsString(twoTierSessionIDs(page), "sess-b") {
			t.Fatal("client filter leaked sess-b")
		}
	})
	t.Run("agent", func(t *testing.T) {
		page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).
			Query("filter-term").Agent(types.Agent("codex")).Build())
		if containsString(twoTierSessionIDs(page), "sess-b") {
			t.Fatal("agent filter leaked sess-b")
		}
	})
	t.Run("kind", func(t *testing.T) {
		page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).
			Query("filter-term").Kind(types.EventKindNote).Build())
		if containsString(twoTierEventIDs(page), "evt-b") {
			t.Fatal("kind filter leaked transcript event")
		}
	})
	t.Run("time", func(t *testing.T) {
		page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).
			Query("filter-term").From(base).To(base.Add(2*time.Hour)).Build())
		if containsString(twoTierSessionIDs(page), "sess-b") || containsString(twoTierEventIDs(page), "evt-b") {
			t.Fatalf("time filter leaked sess-b: events=%v sessions=%v", twoTierEventIDs(page), twoTierSessionIDs(page))
		}
	})
	t.Run("percent is literal", func(t *testing.T) {
		page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).Query("100%").Build())
		if !containsString(twoTierSessionIDs(page), "sess-a") {
			t.Fatalf("literal percent missed sess-a: %v", twoTierSessionIDs(page))
		}
	})
	t.Run("underscore is literal", func(t *testing.T) {
		page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).Query("keep_days").Build())
		if !containsString(twoTierEventIDs(page), "evt-a") {
			t.Fatalf("literal underscore missed evt-a: %v", twoTierEventIDs(page))
		}
	})
	t.Run("ascii fold", func(t *testing.T) {
		page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).Query("NEEDLE").Build())
		if !containsString(twoTierEventIDs(page), "evt-a") {
			t.Fatalf("fold missed evt-a: %v", twoTierEventIDs(page))
		}
	})
	t.Run("no-hit query", func(t *testing.T) {
		page := fx.search(t, apptypes.NewEventSearchCriteriaBuilder(20).Query("xyzzy-nomatch").Build())
		if len(page.Events()) != 0 || len(page.Sessions()) != 0 {
			t.Fatalf("no-hit returned events=%v sessions=%v", twoTierEventIDs(page), twoTierSessionIDs(page))
		}
	})
}

func TestTwoTierSearch_CancelledScanAborts(t *testing.T) {
	t.Parallel()
	fx := newTwoTierFixture(t)
	started := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	fx.seedSession(t, "sess-cancel", "ws", "cli", "codex", started)
	fx.seedEvent(t, "evt-cancel", "sess-cancel", "ws", "cli", "codex", "cancel-term", types.EventKindNote, started.Add(time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fx.events.SearchTwoTier(ctx, apptypes.NewEventSearchCriteriaBuilder(20).Query("cancel-term").Build())
	if err == nil {
		t.Fatal("cancelled search returned nil error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func twoTierSessionTier(page apptypes.TwoTierSearchPage, sessionID string) apptypes.SearchHitTier {
	for _, hit := range page.Sessions() {
		if hit.SessionID().String() == sessionID {
			return hit.Tier()
		}
	}
	return ""
}

func indexOf(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return len(values) + 1
}
