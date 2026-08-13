package cli

import (
	"sort"
	"strings"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

// sessionNode is the in-memory parent/child shape shared by the sessions
// snapshot tree rendering. The standalone `session tree` / `session lineage`
// CLI surfaces were removed in v0.35.0 (#1869).
type sessionNode struct {
	summary  apptypes.SessionSummary
	children []*sessionNode
}

func sortSessionNodes(nodes []*sessionNode) {
	sortSessionNodesWithVisited(nodes, map[string]bool{})
}

func sortSessionNodesWithVisited(nodes []*sessionNode, visited map[string]bool) {
	sort.SliceStable(nodes, func(i, j int) bool {
		return sessionNodeLess(nodes[i], nodes[j])
	})
	for _, node := range nodes {
		sessionID := node.summary.SessionID().String()
		if visited[sessionID] {
			continue
		}
		visited[sessionID] = true
		sortSessionNodesWithVisited(node.children, visited)
		delete(visited, sessionID)
	}
}

func sessionNodeLess(left, right *sessionNode) bool {
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

// keepOngoingLineagesWithOptions prunes every subtree whose sessions are all
// ended so the sessions snapshot surfaces active work without hiding the static
// lineage around it. A parent is retained when either the parent itself is
// active or any descendant is.
type staleLineageOptions struct {
	allowStale bool
	staleAfter time.Duration
	now        time.Time
}

func keepOngoingLineagesWithOptions(roots []*sessionNode, opts staleLineageOptions) []*sessionNode {
	filtered := make([]*sessionNode, 0, len(roots))
	for _, root := range roots {
		if pruneEndedLineages(root, opts) {
			filtered = append(filtered, root)
		}
	}
	return filtered
}

func pruneEndedLineages(node *sessionNode, opts staleLineageOptions) bool {
	keptChildren := node.children[:0]
	for _, child := range node.children {
		if pruneEndedLineages(child, opts) {
			keptChildren = append(keptChildren, child)
		}
	}
	node.children = keptChildren
	return isSessionActive(node.summary) || isSessionStaleAllowed(node.summary, opts) || len(node.children) > 0
}

// isSessionActive treats active and ended_with_late_events sessions as live.
// A session that has not received an end event but has aged past the store
// timeout is already marked status=stale by the SQLite datasource, and
// ongoing-lineage pruning explicitly promises "at least one active session" —
// so stale lineages must not resurface as ongoing work. ended_with_late_events
// counts as live because events arrived after the end marker, meaning the
// session kept going; dropping it would reproduce the #1172 empty snapshot.
func isSessionActive(summary apptypes.SessionSummary) bool {
	switch types.SessionStatus(summary.Status()) {
	case types.SessionStatusActive, types.SessionStatusEndedWithLateEvents:
		return true
	default:
		return false
	}
}

func isSessionStale(summary apptypes.SessionSummary) bool {
	return types.SessionStatus(summary.Status()) == types.SessionStatusStale
}

func isSessionStaleAllowed(summary apptypes.SessionSummary, opts staleLineageOptions) bool {
	if !isSessionStale(summary) {
		return false
	}
	if opts.allowStale {
		return true
	}
	if opts.staleAfter <= 0 {
		return false
	}
	now := opts.now
	if now.IsZero() {
		now = time.Now()
	}
	return !topDataSummaryIsStale(summary, opts.staleAfter, now)
}

// extractSubagentType returns the most specific subagent role the session
// participated in. Hook capture writes hierarchical agent names as
// `<client>/<role>` (for example `claude/Explore` or `claude/planner`), so
// snapshot consumers want a single string to colour or filter on; the helper
// picks the first entry that carries a `/role` suffix and falls back to
// the first agent when none do. The helper always returns an empty string
// for no-agent sessions so JSON fields can omit via `omitempty`.
func extractSubagentType(agents []string) string {
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
