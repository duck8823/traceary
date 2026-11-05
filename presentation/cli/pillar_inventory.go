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
	{Path: "runtime history page", Pillar: pillarKeep, Reason: "explicit bounded committed history page; no core bootstrap, native authority or durable commit cursor"},
	{Path: "runtime metadata export", Pillar: pillarKeep, Reason: "explicit-root persisted metadata audit; no core bootstrap or native liveness authority"},
	{Path: "runtime metadata forget-preview", Pillar: pillarKeep, Reason: "read-only exact loss preview; no implicit cleanup or native effects"},
	{Path: "runtime metadata forget", Pillar: pillarKeep, Reason: "explicit offline historical metadata erasure with both loss acknowledgements; no core or native conversation deletion"},
	{Path: "runtime project register", Pillar: pillarKeep, Reason: "explicit owner-scoped runtime operation; no core Session lifecycle or approval authority"},
	{Path: "runtime worktree register", Pillar: pillarKeep, Reason: "explicit owner-scoped runtime operation; no core Session lifecycle or approval authority"},
	{Path: "runtime registry list", Pillar: pillarKeep, Reason: "explicit owner-scoped runtime operation; no core Session lifecycle or approval authority"},
	{Path: "runtime profile register", Pillar: pillarKeep, Reason: "explicit owner-scoped runtime operation; no core Session lifecycle or approval authority"},
	{Path: "runtime profile list", Pillar: pillarKeep, Reason: "explicit owner-scoped runtime operation; no core Session lifecycle or approval authority"},
	{Path: "runtime resume", Pillar: pillarKeep, Reason: "explicit same-conversation native recovery after managed-owner exit; no pending input replay, global exclusion or core Session lifecycle"},
	{Path: "runtime launch", Pillar: pillarKeep, Reason: "explicit owner-scoped runtime operation; no core Session lifecycle or approval authority"},
	{Path: "runtime input", Pillar: pillarKeep, Reason: "explicit owned-run terminal control/receipt; no telemetry-derived approval or core Session lifecycle"},
	{Path: "runtime resize", Pillar: pillarKeep, Reason: "explicit owned-run terminal control/receipt; no telemetry-derived approval or core Session lifecycle"},
	{Path: "runtime stop", Pillar: pillarKeep, Reason: "explicit owned-run terminal control/receipt; no telemetry-derived approval or core Session lifecycle"},
	{Path: "runtime receipt", Pillar: pillarKeep, Reason: "explicit owned-run terminal control/receipt; no telemetry-derived approval or core Session lifecycle"},
	{Path: "runtime attach", Pillar: pillarKeep, Reason: "bounded owned PTY output only; no child lifecycle or terminal escape interpretation"},
	{Path: "runtime observe", Pillar: pillarKeep, Reason: "bounded untrusted metadata admission; no native binding, input, approval or core store creation"},
	{Path: "runtime list", Pillar: pillarKeep, Reason: "explicit owner-scoped runtime operation; no core Session lifecycle or approval authority"},
	{Path: "daemon serve", Pillar: pillarKeep, Reason: "explicit local runtime transport; not a history or permission authority"},
	{Path: "daemon status", Pillar: pillarKeep, Reason: "bounded local transport readiness; does not query the core store"},
	{Path: "daemon shutdown preview", Pillar: pillarKeep, Reason: "explicit epoch-bound shutdown plan/control; no core store or automatic session resumption"},
	{Path: "daemon shutdown cancel", Pillar: pillarKeep, Reason: "explicit epoch-bound shutdown plan/control; no core store or automatic session resumption"},
	{Path: "daemon shutdown confirm", Pillar: pillarKeep, Reason: "explicit epoch-bound shutdown plan/control; no core store or automatic session resumption"},
	{Path: "daemon shutdown history", Pillar: pillarKeep, Reason: "explicit existing-root lifecycle evidence; no adoption, resumption or core store"},
	{Path: "daemon shutdown receipt", Pillar: pillarKeep, Reason: "explicit epoch-bound shutdown plan/control; no core store or automatic session resumption"},
	{Path: "daemon update status", Pillar: pillarKeep, Reason: "read-only current owner update metadata; no core store or installation"},
	{Path: "daemon update attempt", Pillar: pillarKeep, Reason: "explicit update deferral/notice metadata; no child stop or installation"},
	{Path: "daemon update handoff", Pillar: pillarKeep, Reason: "explicit exact-empty daemon exit; no live work loss, core store or installation"},
	{Path: "daemon autostart status", Pillar: pillarKeep, Reason: "existing-root local preference only; no core store, host inspection or startup"},
	{Path: "daemon autostart opt-in", Pillar: pillarKeep, Reason: "explicit local preference write; no host job installation or agent startup"},
	{Path: "daemon autostart descriptor", Pillar: pillarKeep, Reason: "opt-in-gated descriptor stdout only; no host installation or agent startup"},
	{Path: "audit", Pillar: pillarRecord, Reason: "persist command-audit records"},
	{Path: "log", Pillar: pillarRecord, Reason: "persist a manual event"},
	{Path: "list", Pillar: pillarRecord, Reason: "read recorded events; --follow absorbs tail; --blocks absorbs timeline"},
	{Path: "search", Pillar: pillarRecord, Reason: "search recorded events and session summaries"},
	{Path: "show", Pillar: pillarRecord, Reason: "show one recorded event"},
	{Path: "context", Pillar: pillarRecord, Reason: "assemble recorded context for a session; --handoff absorbs session handoff; --compact-only is the resume summary"},
	{Path: "session start", Pillar: pillarRecord, Reason: "open a recorded session"},
	{Path: "session end", Pillar: pillarRecord, Reason: "close a recorded session"},
	{Path: "session run", Pillar: pillarRecord, Reason: "run a bounded recorded session"},
	{Path: "session refine", Pillar: pillarRecord, Reason: "store the L2 session summary"},
	{Path: "hooks install", Pillar: pillarRecord, Reason: "enable automatic capture; --dry-run absorbs the former hooks print preview and names the expected config path on stderr"},
	{Path: "report", Pillar: pillarRecord, Reason: "period digest of recorded work"},
	{Path: "bundle export", Pillar: pillarKeep, Reason: "export the store (both pillars' data) for transfer"},
	{Path: "bundle import", Pillar: pillarKeep, Reason: "import a transferred store"},
	{Path: "completion", Pillar: pillarKeep, Reason: "shell-completion parent; not a product pillar"},
	{Path: "completion bash", Pillar: pillarKeep, Reason: "Bash completion generator"},
	{Path: "completion fish", Pillar: pillarKeep, Reason: "Fish completion generator"},
	{Path: "completion powershell", Pillar: pillarKeep, Reason: "PowerShell completion generator"},
	{Path: "completion zsh", Pillar: pillarKeep, Reason: "Zsh completion generator"},
	{Path: "doctor", Pillar: pillarKeep, Reason: "install and store health (alias status; --json workspace_identity); prerequisite, not a pillar"},
	{Path: "memory search", Pillar: pillarMemory, Reason: "search durable memories; --all enumerates with former memory list semantics"},
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
	{Path: "store backup create", Pillar: pillarKeep, Reason: "operator safety copy"},
	{Path: "store backup restore", Pillar: pillarKeep, Reason: "restore a safety copy"},
	{Path: "store compact", Pillar: pillarRecord, Reason: "compress, drop retired index, vacuum"},
	{Path: "store compact rollback", Pillar: pillarRecord, Reason: "restore the pre-compact inode"},
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
