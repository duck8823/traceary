package cli

import (
	"encoding/json"
	"io"

	"golang.org/x/xerrors"
)

func writeTopSnapshotJSON(output io.Writer, roots []*sessionNode) error {
	items := make([]*topSnapshotNode, 0, len(roots))
	for _, root := range roots {
		items = append(items, topSnapshotNodeFromSessionNode(root, 0))
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(items); err != nil {
		return xerrors.Errorf("failed to encode top snapshot JSON: %w", err)
	}
	return nil
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
