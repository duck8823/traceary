package cli

import (
	"io"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

// writeTopSnapshotJSON renders the top dashboard snapshot as the
// envelope-wrapped JSON contract added in v0.14.0. The active session
// tree continues to live under `sessions` with its existing field
// shape; `failures`, `recent_commands`, `candidates`, and `stale_memories`
// mirror the new dashboard panes so a script that consumes the snapshot has
// the same data the live dashboard renders.
func writeTopSnapshotJSON(output io.Writer, snap topDataSnapshot) error {
	staleCtx := snapshotStaleContext{
		staleAfter: snap.StaleAfter,
		now:        snap.Now,
	}
	if staleCtx.now.IsZero() {
		staleCtx.now = time.Now()
	}
	sessions := make([]*topSnapshotNode, 0, len(snap.Sessions))
	for _, root := range snap.Sessions {
		sessions = append(sessions, topSnapshotNodeFromSessionNode(root, 0, staleCtx))
	}

	// Recent failure / command panes feed the script-friendly snapshot,
	// so apply the shared list-surface body cap. A multi-hundred-line
	// command_executed payload would otherwise dominate the output and
	// waste host-agent context window. Full content stays available
	// through `traceary show <event_id>`, which intentionally bypasses
	// this limit.
	failures := make([]event, 0, len(snap.Failures))
	for _, ev := range snap.Failures {
		failures = append(failures, newTruncatedEventOutput(ev, apptypes.DefaultTopSnapshotBodyLimit))
	}

	commands := make([]event, 0, len(snap.RecentCommands))
	for _, ev := range snap.RecentCommands {
		commands = append(commands, newTruncatedEventOutput(ev, apptypes.DefaultTopSnapshotBodyLimit))
	}

	candidateItems := make([]memorySummaryOutput, 0, len(snap.Candidates))
	for _, candidate := range snap.Candidates {
		candidateItems = append(candidateItems, newMemorySummaryOutput(candidate))
	}

	staleItems := make([]staleMemoryOutput, 0, len(snap.StaleMemories.Items()))
	for _, stale := range snap.StaleMemories.Items() {
		staleItems = append(staleItems, newStaleMemoryOutput(stale))
	}

	payload := topSnapshotPayload{
		Sessions:       sessions,
		Failures:       failures,
		RecentCommands: commands,
		Candidates: topSnapshotCandidates{
			Count:               len(candidateItems),
			RememberIntentCount: snap.RememberIntentCandidateCount,
			Items:               candidateItems,
		},
		StaleMemories: topSnapshotStale{
			Count: snap.StaleMemories.Count(),
			Items: staleItems,
		},
	}
	return writeJSON(output, payload)
}

// snapshotStaleContext carries the per-snapshot stale-evaluation inputs
// down through the recursive JSON encoder so each node can report its
// own is_stale state without re-reading from globals.
type snapshotStaleContext struct {
	staleAfter time.Duration
	now        time.Time
}

func topSnapshotNodeFromSessionNode(node *sessionNode, depth int, staleCtx snapshotStaleContext) *topSnapshotNode {
	return topSnapshotNodeFromSessionNodeWithVisited(node, depth, map[string]bool{}, staleCtx)
}

func topSnapshotNodeFromSessionNodeWithVisited(node *sessionNode, depth int, visited map[string]bool, staleCtx snapshotStaleContext) *topSnapshotNode {
	s := node.summary
	jn := &topSnapshotNode{
		SessionID:          s.SessionID().String(),
		ParentSessionID:    s.ParentSessionID().String(),
		SpawnEventID:       s.SpawnEventID().String(),
		SubagentKind:       s.SubagentKind(),
		Depth:              depth,
		Workspace:          s.Workspace().String(),
		LatestEventKind:    s.LatestEventKind().String(),
		LatestEventMessage: s.LatestEventMessage(),
		LatestEventAt:      formatJSONTime(s.LatestEventAt()),
		Label:              s.Label(),
		Summary:            s.Summary(),
		StartedAt:          formatJSONTime(s.StartedAt()),
		Status:             s.Status(),
		TotalEvents:        s.TotalEvents(),
		CommandCount:       s.CommandCount(),
		Agents:             s.Agents(),
		SubagentType:       extractSubagentType(s.Agents()),
		Children:           make([]*topSnapshotNode, 0, len(node.children)),
	}
	if spawnOrder, ok := s.SpawnOrder().Value(); ok {
		jn.SpawnOrder = &spawnOrder
	}
	if endedAt, ok := s.EndedAt().Value(); ok {
		endStr := formatJSONTime(endedAt)
		jn.EndedAt = &endStr
		secs := endedAt.Sub(s.StartedAt()).Seconds()
		jn.DurationSec = &secs
	}
	if topDataSummaryIsStale(s, staleCtx.staleAfter, staleCtx.now) {
		jn.IsStale = true
		staleAfterSec := staleCtx.staleAfter.Seconds()
		jn.StaleAfterSec = &staleAfterSec
		staleAgeSec := staleCtx.now.Sub(s.StartedAt()).Seconds() - staleAfterSec
		if staleAgeSec < 0 {
			staleAgeSec = 0
		}
		jn.StaleAgeSec = &staleAgeSec
	}
	if visited[s.SessionID().String()] {
		jn.Status = "cycle-detected"
		return jn
	}
	visited[s.SessionID().String()] = true
	for _, child := range node.children {
		jn.Children = append(jn.Children, topSnapshotNodeFromSessionNodeWithVisited(child, depth+1, visited, staleCtx))
	}
	delete(visited, s.SessionID().String())
	return jn
}
