package cli

import (
	"encoding/json"
	"io"

	"golang.org/x/xerrors"
)

type topSnapshotNode struct {
	SessionID          string             `json:"session_id"`
	ParentSessionID    string             `json:"parent_session_id,omitempty"`
	SpawnEventID       string             `json:"spawn_event_id,omitempty"`
	SubagentKind       string             `json:"subagent_kind,omitempty"`
	SpawnOrder         *int               `json:"spawn_order,omitempty"`
	Depth              int                `json:"depth"`
	Workspace          string             `json:"workspace"`
	LatestEventKind    string             `json:"latest_event_kind"`
	LatestEventMessage string             `json:"latest_event_message"`
	LatestEventAt      string             `json:"latest_event_at"`
	Label              string             `json:"label,omitempty"`
	Summary            string             `json:"summary,omitempty"`
	StartedAt          string             `json:"started_at"`
	EndedAt            *string            `json:"ended_at,omitempty"`
	Status             string             `json:"status"`
	DurationSec        *float64           `json:"duration_sec,omitempty"`
	TotalEvents        int                `json:"total_events"`
	CommandCount       int                `json:"command_count"`
	Agents             []string           `json:"agents"`
	SubagentType       string             `json:"subagent_type,omitempty"`
	Children           []*topSnapshotNode `json:"children"`
}

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
