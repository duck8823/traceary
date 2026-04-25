package mcpserver

import (
	"sort"
	"strings"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

const maxSessionTreeDepth = 100

type sessionLineageNode struct {
	summary  apptypes.SessionSummary
	children []*sessionLineageNode
}

func newSessionLineageOutput(summaries []apptypes.SessionSummary) sessionLineageOutput {
	_, roots := buildSessionLineageNodes(summaries)
	output := sessionLineageOutput{Roots: make([]sessionLineageNodeOutput, 0, len(roots))}
	for _, root := range roots {
		output.Roots = append(output.Roots, sessionLineageNodeToOutput(root, 0))
	}
	return output
}

func newSessionTreeOutput(summaries []apptypes.SessionSummary, rootSessionID string, depthLimit *int) []sessionLineageNodeOutput {
	nodeMap, _ := buildSessionLineageNodes(summaries)
	root, ok := nodeMap[rootSessionID]
	if !ok {
		return []sessionLineageNodeOutput{}
	}
	return []sessionLineageNodeOutput{sessionLineageNodeToOutputWithDepthLimit(root, 0, effectiveSessionTreeDepthLimit(depthLimit), map[string]bool{})}
}

func buildSessionLineageNodes(summaries []apptypes.SessionSummary) (map[string]*sessionLineageNode, []*sessionLineageNode) {
	nodeMap := make(map[string]*sessionLineageNode, len(summaries))
	roots := make([]*sessionLineageNode, 0)
	for _, summary := range summaries {
		nodeMap[summary.SessionID().String()] = &sessionLineageNode{summary: summary}
	}
	for _, summary := range summaries {
		node := nodeMap[summary.SessionID().String()]
		if parentID := summary.ParentSessionID().String(); parentID != "" {
			if parent, ok := nodeMap[parentID]; ok {
				parent.children = append(parent.children, node)
				continue
			}
		}
		roots = append(roots, node)
	}
	sortSessionLineageNodes(roots)
	return nodeMap, roots
}

func sortSessionLineageNodes(nodes []*sessionLineageNode) {
	sortSessionLineageNodesWithVisited(nodes, map[string]bool{})
}

func sortSessionLineageNodesWithVisited(nodes []*sessionLineageNode, visited map[string]bool) {
	sort.SliceStable(nodes, func(i, j int) bool {
		return sessionLineageNodeLess(nodes[i], nodes[j])
	})
	for _, node := range nodes {
		sessionID := node.summary.SessionID().String()
		if visited[sessionID] {
			continue
		}
		visited[sessionID] = true
		sortSessionLineageNodesWithVisited(node.children, visited)
		delete(visited, sessionID)
	}
}

func sessionLineageNodeLess(left, right *sessionLineageNode) bool {
	leftOrder, leftHasOrder := left.summary.SpawnOrder().Value()
	rightOrder, rightHasOrder := right.summary.SpawnOrder().Value()
	if leftHasOrder && rightHasOrder && leftOrder != rightOrder {
		return leftOrder < rightOrder
	}
	if leftHasOrder != rightHasOrder {
		return leftHasOrder
	}
	if !left.summary.StartedAt().Equal(right.summary.StartedAt()) {
		return left.summary.StartedAt().Before(right.summary.StartedAt())
	}
	return false
}

func sessionLineageNodeToOutput(node *sessionLineageNode, depth int) sessionLineageNodeOutput {
	return sessionLineageNodeToOutputWithDepthLimit(node, depth, maxSessionTreeDepth, map[string]bool{})
}

func effectiveSessionTreeDepthLimit(depthLimit *int) int {
	if depthLimit == nil || *depthLimit > maxSessionTreeDepth {
		return maxSessionTreeDepth
	}
	return *depthLimit
}

func sessionLineageNodeToOutputWithDepthLimit(node *sessionLineageNode, depth int, depthLimit int, visited map[string]bool) sessionLineageNodeOutput {
	summary := node.summary
	sessionID := summary.SessionID().String()
	cycleDetected := visited[sessionID]
	output := sessionLineageNodeOutput{
		SessionID:       sessionID,
		ParentSessionID: summary.ParentSessionID().String(),
		SpawnEventID:    summary.SpawnEventID().String(),
		SubagentKind:    summary.SubagentKind(),
		Depth:           depth,
		Workspace:       summary.Workspace().String(),
		Label:           summary.Label(),
		Summary:         summary.Summary(),
		StartedAt:       summary.StartedAt().UTC().Format(time.RFC3339Nano),
		Status:          summary.Status(),
		TotalEvents:     summary.TotalEvents(),
		CommandCount:    summary.CommandCount(),
		Agents:          summary.Agents(),
		SubagentType:    sessionLineageSubagentType(summary.Agents()),
		Children:        make([]sessionLineageNodeOutput, 0, len(node.children)),
	}
	if spawnOrder, ok := summary.SpawnOrder().Value(); ok {
		output.SpawnOrder = &spawnOrder
	}
	if endedAt, ok := summary.EndedAt().Value(); ok {
		endedAtString := endedAt.UTC().Format(time.RFC3339Nano)
		output.EndedAt = &endedAtString
		durationSec := endedAt.Sub(summary.StartedAt()).Seconds()
		output.DurationSec = &durationSec
	}
	if cycleDetected {
		output.Status = "cycle-detected"
		return output
	}
	if depth < depthLimit {
		visited[sessionID] = true
		for _, child := range node.children {
			output.Children = append(output.Children, sessionLineageNodeToOutputWithDepthLimit(child, depth+1, depthLimit, visited))
		}
		delete(visited, sessionID)
	}
	return output
}

func sessionLineageSubagentType(agents []string) string {
	if len(agents) == 0 {
		return ""
	}
	for _, agent := range agents {
		if strings.Contains(agent, "/") {
			return agent
		}
	}
	return agents[0]
}
