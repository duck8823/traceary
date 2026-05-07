package cli

import (
	"io"
)

// writeTopSnapshotJSON renders the top dashboard snapshot as the
// envelope-wrapped JSON contract added in v0.14.0. The active session
// tree continues to live under `sessions` with its existing field
// shape; `failures`, `recent_commands`, and `candidates` mirror the
// new dashboard panes so a script that consumes the snapshot has the
// same data the live dashboard renders.
func writeTopSnapshotJSON(output io.Writer, snap topDataSnapshot) error {
	sessions := make([]*topSnapshotNode, 0, len(snap.Sessions))
	for _, root := range snap.Sessions {
		sessions = append(sessions, topSnapshotNodeFromSessionNode(root, 0))
	}

	failures := make([]event, 0, len(snap.Failures))
	for _, ev := range snap.Failures {
		failures = append(failures, newEventOutput(ev))
	}

	commands := make([]event, 0, len(snap.RecentCommands))
	for _, ev := range snap.RecentCommands {
		commands = append(commands, newEventOutput(ev))
	}

	candidateItems := make([]memorySummaryOutput, 0, len(snap.Candidates))
	for _, candidate := range snap.Candidates {
		candidateItems = append(candidateItems, newMemorySummaryOutput(candidate))
	}

	payload := topSnapshotPayload{
		Sessions:       sessions,
		Failures:       failures,
		RecentCommands: commands,
		Candidates: topSnapshotCandidates{
			Count: len(candidateItems),
			Items: candidateItems,
		},
	}
	return writeJSON(output, payload)
}

func topSnapshotNodeFromSessionNode(node *sessionNode, depth int) *topSnapshotNode {
	return topSnapshotNodeFromSessionNodeWithVisited(node, depth, map[string]bool{})
}

func topSnapshotNodeFromSessionNodeWithVisited(node *sessionNode, depth int, visited map[string]bool) *topSnapshotNode {
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
	if visited[s.SessionID().String()] {
		jn.Status = "cycle-detected"
		return jn
	}
	visited[s.SessionID().String()] = true
	for _, child := range node.children {
		jn.Children = append(jn.Children, topSnapshotNodeFromSessionNodeWithVisited(child, depth+1, visited))
	}
	delete(visited, s.SessionID().String())
	return jn
}
