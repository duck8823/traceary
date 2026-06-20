package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// TestContentEventReliabilityFindingsDetectDuplicateGroups confirms that
// near-simultaneous identical prompt/transcript hook events (the #1205 Codex
// double-write) are reported as one duplicate group, and that the rendered check
// surfaces the event IDs without leaking the body.
func TestContentEventReliabilityFindingsDetectDuplicateGroups(t *testing.T) {
	largeBody := strings.Repeat("p", 2048)
	base := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	events := []*model.Event{
		mustContentEvent(t, "evt-a", types.EventKindPrompt, "session-1", "workspace-1", "user_prompt_submit", largeBody, base),
		mustContentEvent(t, "evt-b", types.EventKindPrompt, "session-1", "workspace-1", "user_prompt_submit", largeBody, base.Add(time.Second)),
	}

	findings := contentEventReliabilityFindingsFromEvents(events, false)
	if findings.ScannedContentCount != 2 {
		t.Fatalf("ScannedContentCount = %d, want 2", findings.ScannedContentCount)
	}
	if len(findings.DuplicateGroups) != 1 || findings.DuplicateGroups[0].Count != 2 {
		t.Fatalf("DuplicateGroups = %+v, want one group of 2", findings.DuplicateGroups)
	}
	if findings.DuplicateGroups[0].Kind != "prompt" || findings.DuplicateGroups[0].SourceHook != "user_prompt_submit" {
		t.Fatalf("group metadata = %+v, want kind=prompt source_hook=user_prompt_submit", findings.DuplicateGroups[0])
	}

	check := contentEventReliabilityCheckFromFindings(findings, false)
	if check.Status != doctorStatusWarn {
		t.Fatalf("check status = %q, want warn", check.Status)
	}
	if !strings.Contains(check.Message, "evt-a") || !strings.Contains(check.Message, "evt-b") {
		t.Fatalf("check message = %q, want sampled event IDs", check.Message)
	}
	if strings.Contains(check.Message, largeBody) {
		t.Fatalf("check message leaked full content body")
	}
	if !strings.Contains(check.Hint, "traceary show <event_id>") {
		t.Fatalf("check hint = %q, want `traceary show <event_id>` guidance", check.Hint)
	}
}

// TestContentEventReliabilityFindingsDetectTranscriptDuplicates ensures the
// transcript kind is also covered and kept distinct from prompt.
func TestContentEventReliabilityFindingsDetectTranscriptDuplicates(t *testing.T) {
	base := time.Date(2026, 6, 20, 1, 0, 0, 0, time.UTC)
	events := []*model.Event{
		mustContentEvent(t, "evt-t1", types.EventKindTranscript, "session-1", "workspace-1", "stop", "assistant turn", base),
		mustContentEvent(t, "evt-t2", types.EventKindTranscript, "session-1", "workspace-1", "stop", "assistant turn", base.Add(time.Second)),
		// Same body but prompt kind must not merge with the transcript group.
		mustContentEvent(t, "evt-p1", types.EventKindPrompt, "session-1", "workspace-1", "stop", "assistant turn", base),
	}

	findings := contentEventReliabilityFindingsFromEvents(events, false)
	if len(findings.DuplicateGroups) != 1 || findings.DuplicateGroups[0].Count != 2 {
		t.Fatalf("DuplicateGroups = %+v, want one transcript group of 2", findings.DuplicateGroups)
	}
	if findings.DuplicateGroups[0].Kind != "transcript" {
		t.Fatalf("group kind = %q, want transcript", findings.DuplicateGroups[0].Kind)
	}
}

// TestContentEventReliabilityFindingsIgnoreFarApartByDefault covers the
// false-positive guard: identical content re-sent minutes apart is a deliberate
// repeat, not a hook double-write, so it must NOT warn by default.
func TestContentEventReliabilityFindingsIgnoreFarApartByDefault(t *testing.T) {
	base := time.Date(2026, 6, 20, 2, 0, 0, 0, time.UTC)
	events := []*model.Event{
		mustContentEvent(t, "evt-1", types.EventKindPrompt, "session-1", "workspace-1", "user_prompt_submit", "run the tests", base),
		mustContentEvent(t, "evt-2", types.EventKindPrompt, "session-1", "workspace-1", "user_prompt_submit", "run the tests", base.Add(9*time.Minute)),
	}

	findings := contentEventReliabilityFindingsFromEvents(events, false)
	if len(findings.DuplicateGroups) != 0 {
		t.Fatalf("DuplicateGroups = %+v, want none for far-apart repeats", findings.DuplicateGroups)
	}
	check := contentEventReliabilityCheckFromFindings(findings, false)
	if check.Status != doctorStatusPass {
		t.Fatalf("check status = %q, want pass", check.Status)
	}
}

// TestContentEventReliabilityFindingsDistinctBodiesNotGrouped verifies that
// different bodies (even near-simultaneous) are never grouped.
func TestContentEventReliabilityFindingsDistinctBodiesNotGrouped(t *testing.T) {
	base := time.Date(2026, 6, 20, 3, 0, 0, 0, time.UTC)
	events := []*model.Event{
		mustContentEvent(t, "evt-1", types.EventKindPrompt, "session-1", "workspace-1", "user_prompt_submit", "first prompt", base),
		mustContentEvent(t, "evt-2", types.EventKindPrompt, "session-1", "workspace-1", "user_prompt_submit", "second prompt", base.Add(time.Second)),
	}

	findings := contentEventReliabilityFindingsFromEvents(events, false)
	if len(findings.DuplicateGroups) != 0 {
		t.Fatalf("DuplicateGroups = %+v, want none for distinct bodies", findings.DuplicateGroups)
	}
}

// TestContentEventReliabilityFindingsStrictReportsFarApart confirms strict mode
// reports the whole exact group regardless of time gap.
func TestContentEventReliabilityFindingsStrictReportsFarApart(t *testing.T) {
	base := time.Date(2026, 6, 20, 4, 0, 0, 0, time.UTC)
	newEvents := func() []*model.Event {
		return []*model.Event{
			mustContentEvent(t, "evt-near-1", types.EventKindPrompt, "session-1", "workspace-1", "user_prompt_submit", "hello", base),
			mustContentEvent(t, "evt-near-2", types.EventKindPrompt, "session-1", "workspace-1", "user_prompt_submit", "hello", base.Add(time.Second)),
			mustContentEvent(t, "evt-far", types.EventKindPrompt, "session-1", "workspace-1", "user_prompt_submit", "hello", base.Add(20*time.Minute)),
		}
	}

	defaultFindings := contentEventReliabilityFindingsFromEvents(newEvents(), false)
	if len(defaultFindings.DuplicateGroups) != 1 || defaultFindings.DuplicateGroups[0].Count != 2 {
		t.Fatalf("default DuplicateGroups = %+v, want only the near-simultaneous pair", defaultFindings.DuplicateGroups)
	}

	strictFindings := contentEventReliabilityFindingsFromEvents(newEvents(), true)
	if len(strictFindings.DuplicateGroups) != 1 || strictFindings.DuplicateGroups[0].Count != 3 {
		t.Fatalf("strict DuplicateGroups = %+v, want one exact group of 3", strictFindings.DuplicateGroups)
	}
}

// TestContentEventReliabilityFindingsExcludeNonHookAndCommand pins acceptance
// criterion #3: command_executed events and non-hook content writes never affect
// this diagnostic.
func TestContentEventReliabilityFindingsExcludeNonHookAndCommand(t *testing.T) {
	base := time.Date(2026, 6, 20, 5, 0, 0, 0, time.UTC)
	events := []*model.Event{
		// command_executed (even client="hook") must be ignored.
		mustEventWithClient(t, "evt-cmd-1", "hook", types.EventKindCommandExecuted, "session-1", "workspace-1", "audit", "go test ./...", base),
		mustEventWithClient(t, "evt-cmd-2", "hook", types.EventKindCommandExecuted, "session-1", "workspace-1", "audit", "go test ./...", base.Add(time.Second)),
		// non-hook content writes (direct CLI/MCP) must be ignored.
		mustEventWithClient(t, "evt-cli-1", "cli", types.EventKindPrompt, "session-1", "workspace-1", "", "note body", base),
		mustEventWithClient(t, "evt-cli-2", "cli", types.EventKindPrompt, "session-1", "workspace-1", "", "note body", base.Add(time.Second)),
	}

	findings := contentEventReliabilityFindingsFromEvents(events, false)
	if findings.ScannedContentCount != 0 {
		t.Fatalf("ScannedContentCount = %d, want 0 (command/non-hook excluded)", findings.ScannedContentCount)
	}
	if len(findings.DuplicateGroups) != 0 {
		t.Fatalf("DuplicateGroups = %+v, want none", findings.DuplicateGroups)
	}
}

// TestContentEventReliabilityFindingsNormalizeTrailingWhitespace verifies bodies
// differing only by trailing whitespace are treated as identical.
func TestContentEventReliabilityFindingsNormalizeTrailingWhitespace(t *testing.T) {
	base := time.Date(2026, 6, 20, 6, 0, 0, 0, time.UTC)
	events := []*model.Event{
		mustContentEvent(t, "evt-1", types.EventKindPrompt, "session-1", "workspace-1", "user_prompt_submit", "same body", base),
		mustContentEvent(t, "evt-2", types.EventKindPrompt, "session-1", "workspace-1", "user_prompt_submit", "same body\n", base.Add(time.Second)),
	}

	findings := contentEventReliabilityFindingsFromEvents(events, false)
	if len(findings.DuplicateGroups) != 1 || findings.DuplicateGroups[0].Count != 2 {
		t.Fatalf("DuplicateGroups = %+v, want one group of 2 after normalization", findings.DuplicateGroups)
	}
}

func mustContentEvent(t *testing.T, eventID string, kind types.EventKind, sessionID, workspace, sourceHook, body string, createdAt time.Time) *model.Event {
	t.Helper()
	return mustEventWithClient(t, eventID, "hook", kind, sessionID, workspace, sourceHook, body, createdAt)
}

func mustEventWithClient(t *testing.T, eventID, client string, kind types.EventKind, sessionID, workspace, sourceHook, body string, createdAt time.Time) *model.Event {
	t.Helper()
	return model.EventOfWithSourceHook(
		types.EventID(eventID),
		kind,
		types.Client(client),
		types.Agent("codex"),
		types.SessionID(sessionID),
		types.Workspace(workspace),
		body,
		createdAt,
		sourceHook,
	)
}
