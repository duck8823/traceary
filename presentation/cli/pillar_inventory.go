package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// pillarKind is which of the two product pillars a visible command serves.
// keep is the explicit third state: neither pillar, with a written reason.
type pillarKind string

const (
	pillarRecord pillarKind = "record"
	pillarMemory pillarKind = "memory"
	pillarKeep   pillarKind = "keep"
)

// pillarInventoryEntry is one visible public or admin operator action.
// Hidden hook plumbing is not listed. RemovalTarget is empty unless this
// sweep publishes a one-minor notice.
type pillarInventoryEntry struct {
	Path          string
	Pillar        pillarKind
	Reason        string
	RemovalTarget string
	Replacement   string
}

// pillarInventory is the visible public/admin surface. Every surviving
// visible leaf maps to 記録 (record), 記憶 (memory), or a keep-reason.
// Usage counts are not grounds. memory store remember was removed in
// v0.36.0 (#1870) after the v0.35 notice.
var pillarInventory = []pillarInventoryEntry{
	{Path: "audit", Pillar: pillarRecord, Reason: "persist command-audit records"},
	{Path: "log", Pillar: pillarRecord, Reason: "persist a manual event"},
	{Path: "list", Pillar: pillarRecord, Reason: "read recorded events; --follow absorbs the former tail live stream"},
	{Path: "search", Pillar: pillarRecord, Reason: "search recorded events and session summaries"},
	{Path: "timeline", Pillar: pillarRecord, Reason: "workspace timeline of recorded work; list --blocks does not exist"},
	{Path: "show", Pillar: pillarRecord, Reason: "show one recorded event"},
	{Path: "context", Pillar: pillarRecord, Reason: "assemble recorded context for a session"},
	{Path: "session start", Pillar: pillarRecord, Reason: "open a recorded session"},
	{Path: "session end", Pillar: pillarRecord, Reason: "close a recorded session"},
	{Path: "session run", Pillar: pillarRecord, Reason: "run a bounded recorded session"},
	{Path: "session refine", Pillar: pillarRecord, Reason: "store the L2 summary that makes transcript bodies discardable"},
	{Path: "session handoff", Pillar: pillarRecord, Reason: "structured session summary for the next session; context does not absorb it"},
	{Path: "session gc", Pillar: pillarRecord, Reason: "close stale sessions (evict)"},
	{Path: "session repair-one-shot", Pillar: pillarRecord, Reason: "repair one-shot classification of recorded sessions"},
	{Path: "hooks install", Pillar: pillarRecord, Reason: "enable automatic capture"},
	{Path: "hooks print", Pillar: pillarRecord, Reason: "preview hook scripts without writing; hooks install --dry-run does not exist"},
	{Path: "hooks guide", Pillar: pillarKeep, Reason: "operator documentation for hook setup; not itself capture or memory"},
	{Path: "replay", Pillar: pillarKeep, Reason: "only single-file HTML export of recorded sessions; #1870 called usage a weak removal basis and there is no replacement"},
	{Path: "report", Pillar: pillarRecord, Reason: "period digest of recorded work"},
	{Path: "report workspace-identity", Pillar: pillarKeep, Reason: "workspace-attribution diagnostics; doctor does not absorb this report"},
	{Path: "bundle export", Pillar: pillarKeep, Reason: "export the store (both pillars' data) for transfer"},
	{Path: "bundle import", Pillar: pillarKeep, Reason: "import a transferred store"},
	{Path: "completion", Pillar: pillarKeep, Reason: "shell-completion parent; not a product pillar"},
	{Path: "completion bash", Pillar: pillarKeep, Reason: "Bash completion generator"},
	{Path: "completion fish", Pillar: pillarKeep, Reason: "Fish completion generator"},
	{Path: "completion powershell", Pillar: pillarKeep, Reason: "PowerShell completion generator"},
	{Path: "completion zsh", Pillar: pillarKeep, Reason: "Zsh completion generator"},
	{Path: "doctor", Pillar: pillarKeep, Reason: "install and store health (alias status); prerequisite, not a pillar"},
	{Path: "memory list", Pillar: pillarMemory, Reason: "list durable memories; search --all does not exist"},
	{Path: "memory search", Pillar: pillarMemory, Reason: "search durable memories"},
	{Path: "memory show", Pillar: pillarMemory, Reason: "show one durable memory"},
	{Path: "memory inbox list", Pillar: pillarMemory, Reason: "list memory candidates awaiting review"},
	{Path: "memory inbox show", Pillar: pillarMemory, Reason: "evidence-first card for one candidate"},
	{Path: "memory inbox accept", Pillar: pillarMemory, Reason: "accept a reviewed candidate"},
	{Path: "memory inbox reject", Pillar: pillarMemory, Reason: "reject a reviewed candidate"},
	{Path: "memory inbox attach", Pillar: pillarMemory, Reason: "attach evidence or artifact refs to a candidate"},
	{Path: "memory inbox cleanup", Pillar: pillarMemory, Reason: "bulk-reject stale or low-quality candidates; not the same transition as decay"},
	{Path: "memory inbox restore", Pillar: pillarMemory, Reason: "restore expired memories to candidates"},
	{Path: "memory inbox review", Pillar: pillarMemory, Reason: "TTY review of the memory review queue"},
	{Path: "memory store propose", Pillar: pillarMemory, Reason: "write a candidate; the skill-facing remember path"},
	{Path: "memory store distill", Pillar: pillarMemory, Reason: "operator-authored accepted fact from existing candidates"},
	{Path: "memory decay", Pillar: pillarMemory, Reason: "expire stale auto-extracted candidates; distinct from inbox cleanup reject"},
	{Path: "memory admin extract", Pillar: pillarMemory, Reason: "extract candidates from a recorded session"},
	{Path: "memory admin import codex", Pillar: pillarMemory, Reason: "import host Codex memories as candidates"},
	{Path: "memory admin import instructions", Pillar: pillarMemory, Reason: "import host instruction bullets as candidates"},
	{Path: "memory admin export", Pillar: pillarMemory, Reason: "export accepted memories to a host instruction file"},
	{Path: "memory admin activate", Pillar: pillarMemory, Reason: "plan host-native memory activation"},
	{Path: "memory admin hygiene scan", Pillar: pillarMemory, Reason: "scan memories for hygiene issues"},
	{Path: "memory admin hygiene apply", Pillar: pillarMemory, Reason: "apply hygiene suggestions by id"},
	{Path: "memory admin supersede", Pillar: pillarMemory, Reason: "replace an accepted memory"},
	{Path: "memory admin expire", Pillar: pillarMemory, Reason: "expire an accepted memory"},
	{Path: "memory admin set-validity", Pillar: pillarMemory, Reason: "set a memory validity window"},
	{Path: "store init", Pillar: pillarKeep, Reason: "create the SQLite store; prerequisite"},
	{Path: "store backup create", Pillar: pillarKeep, Reason: "operator safety copy"},
	{Path: "store backup restore", Pillar: pillarKeep, Reason: "restore a safety copy"},
	{Path: "store archive create", Pillar: pillarKeep, Reason: "offsite archive of the store; not folded into compact"},
	{Path: "store archive restore", Pillar: pillarKeep, Reason: "restore an archive"},
	{Path: "store archive verify", Pillar: pillarKeep, Reason: "verify an archive"},
	{Path: "store capacity", Pillar: pillarKeep, Reason: "metadata-only growth diagnostics; doctor does not absorb it"},
	{Path: "store compact", Pillar: pillarRecord, Reason: "compress, drop retired index, discard covered bodies, vacuum"},
	{Path: "store compact rollback", Pillar: pillarRecord, Reason: "restore the pre-compact inode"},
	{Path: "store retention files plan", Pillar: pillarKeep, Reason: "plan filesystem retention of backup/archive files; distinct from body retention folded into compact"},
	{Path: "store retention files apply", Pillar: pillarKeep, Reason: "apply filesystem file retention"},
	{Path: "store search-projection start", Pillar: pillarRecord, Reason: "build the search index over recorded events"},
	{Path: "store search-projection resume", Pillar: pillarRecord, Reason: "resume a search-projection generation"},
	{Path: "store search-projection status", Pillar: pillarRecord, Reason: "inspect search-projection progress"},
	{Path: "store search-projection abort", Pillar: pillarRecord, Reason: "park a search-projection generation"},
	{Path: "store search-projection probe", Pillar: pillarRecord, Reason: "probe search-projection capacity without starting a generation"},
	{Path: "store workspace-alias add", Pillar: pillarKeep, Reason: "live alias mechanism; Wave 4 of #1693 found 147 distinct conflict pairs, not empty backing"},
	{Path: "store workspace-alias list", Pillar: pillarKeep, Reason: "list workspace aliases"},
	{Path: "store workspace-alias remove", Pillar: pillarKeep, Reason: "remove a workspace alias"},
}

func applyInventoryDeprecations(root *cobra.Command) {
	for _, entry := range pillarInventory {
		if entry.RemovalTarget == "" {
			continue
		}
		cmd := lookupCommandPath(root, entry.Path)
		if cmd == nil {
			continue
		}
		applyCommandDeprecation(cmd, entry.Replacement, entry.RemovalTarget)
	}
}

func lookupCommandPath(root *cobra.Command, path string) *cobra.Command {
	current := root
	for _, part := range strings.Fields(path) {
		var next *cobra.Command
		for _, child := range current.Commands() {
			if child.Name() == part {
				next = child
				break
			}
		}
		if next == nil {
			return nil
		}
		current = next
	}
	if current == root {
		return nil
	}
	return current
}
