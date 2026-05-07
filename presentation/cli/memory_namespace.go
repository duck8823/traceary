package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newMemoryStoreCommand groups the deliberate write/store surface
// (`remember`, `propose`, `distill`) under `traceary memory store` so
// every command that writes a durable-memory row sits in one
// namespace, regardless of whether the row lands as accepted or
// candidate.
func (c *RootCLI) newMemoryStoreCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "store",
		Short: Localize("Record, propose, and distill durable memories", "durable memory の記録・propose・distill を行う"),
	}
	cmd.AddCommand(c.newMemoryRememberCommand())
	cmd.AddCommand(c.newMemoryProposeCommand())
	cmd.AddCommand(c.newMemoryDistillCommand())
	return cmd
}

// newMemoryAdminCommand groups host-side and maintenance commands —
// extraction, import/export, activation, hygiene, graph, and the
// lifecycle verbs (`supersede`, `expire`, `set-validity`) — under
// `traceary memory admin` so operator-facing concerns sit together.
func (c *RootCLI) newMemoryAdminCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: Localize("Run admin and host-side durable-memory operations", "durable memory の admin / host 連携操作を行う"),
	}
	cmd.AddCommand(c.newMemoryExtractCommand())
	cmd.AddCommand(c.newMemoryImportCommand())
	cmd.AddCommand(c.newMemoryExportCommand())
	cmd.AddCommand(c.newMemoryActivateCommand())
	cmd.AddCommand(c.newMemoryHygieneCommand())
	cmd.AddCommand(c.newMemoryGraphCommand())
	cmd.AddCommand(c.newMemorySupersedeCommand())
	cmd.AddCommand(c.newMemoryExpireCommand())
	cmd.AddCommand(c.newMemorySetValidityCommand())
	return cmd
}

// addDeprecatedMemoryAliases registers the legacy flat memory paths as
// hidden Cobra commands under `traceary memory`. Each alias still
// executes the original use case but emits a one-line deprecation
// notice on stderr that names the canonical replacement and the v0.15
// removal target. JSON / stdout output is unchanged so scripted callers
// keep working through one release of overlap.
func (c *RootCLI) addDeprecatedMemoryAliases(memoryCmd *cobra.Command) {
	type alias struct {
		cmd         *cobra.Command
		replacement string
	}
	aliases := []alias{
		{cmd: c.newMemoryAcceptCommand(), replacement: "traceary memory inbox accept --ids <id>"},
		{cmd: c.newMemoryRejectCommand(), replacement: "traceary memory inbox reject --ids <id>"},
		{cmd: c.newMemoryRememberCommand(), replacement: "traceary memory store remember"},
		{cmd: c.newMemoryProposeCommand(), replacement: "traceary memory store propose"},
		{cmd: c.newMemoryDistillCommand(), replacement: "traceary memory store distill"},
		{cmd: c.newMemoryExtractCommand(), replacement: "traceary memory admin extract"},
		{cmd: c.newMemorySupersedeCommand(), replacement: "traceary memory admin supersede"},
		{cmd: c.newMemoryExpireCommand(), replacement: "traceary memory admin expire"},
		{cmd: c.newMemorySetValidityCommand(), replacement: "traceary memory admin set-validity"},
		{cmd: c.newMemoryImportCommand(), replacement: "traceary memory admin import"},
		{cmd: c.newMemoryExportCommand(), replacement: "traceary memory admin export"},
		{cmd: c.newMemoryActivateCommand(), replacement: "traceary memory admin activate"},
		{cmd: c.newMemoryHygieneCommand(), replacement: "traceary memory admin hygiene"},
		{cmd: c.newMemoryGraphCommand(), replacement: "traceary memory admin graph"},
	}
	for _, entry := range aliases {
		applyMemoryAliasDeprecation(entry.cmd, entry.replacement)
		memoryCmd.AddCommand(entry.cmd)
	}
}

// applyMemoryAliasDeprecation hides cmd from the parent's `--help`
// output and installs a PersistentPreRun that emits a single-line
// stderr deprecation notice naming the canonical replacement and the
// v0.15 removal target. PersistentPreRun (rather than PreRun) so
// subcommands of grouped aliases — `memory hygiene scan`, `memory
// graph add` — still trigger the notice.
//
// When the executing command is a subcommand of the alias root (e.g.
// `memory hygiene scan` under the `hygiene` alias), the runtime notice
// names the precise canonical path (`traceary memory admin hygiene
// scan`) so users can copy-paste the replacement directly. The Long
// text on the alias root keeps the bare-parent replacement to avoid
// over-promising in `--help` output.
//
// Cobra's built-in `Deprecated` field routes its warning through
// `cmd.Printf` which targets stdout. The aliases must keep stdout /
// JSON byte-for-byte compatible for scripted callers, so we emit the
// notice ourselves to stderr instead.
func applyMemoryAliasDeprecation(cmd *cobra.Command, replacement string) {
	cmd.Hidden = true
	rootNotice := deprecationNoticeFor(replacement)
	if cmd.Long != "" {
		cmd.Long = rootNotice + "\n\n" + cmd.Long
	} else if cmd.Short != "" {
		cmd.Long = rootNotice + "\n\n" + cmd.Short
	} else {
		cmd.Long = rootNotice
	}
	existing := cmd.PersistentPreRun
	cmd.PersistentPreRun = func(c *cobra.Command, args []string) {
		runtimeReplacement := replacement
		if c != cmd {
			// c is a descendant of the alias root (e.g. `scan` under
			// `hygiene`). Walk up to cmd, collecting the subcommand
			// path so the notice names the exact canonical equivalent.
			var suffix []string
			for cur := c; cur != nil && cur != cmd; cur = cur.Parent() {
				suffix = append([]string{cur.Name()}, suffix...)
			}
			for _, name := range suffix {
				runtimeReplacement += " " + name
			}
		}
		// Best-effort stderr write: pipelines redirect stderr to
		// /dev/null in headless contexts, and a closed stderr is not
		// worth failing the command over.
		_, _ = fmt.Fprintln(c.ErrOrStderr(), deprecationNoticeFor(runtimeReplacement))
		if existing != nil {
			existing(c, args)
		}
	}
}

func deprecationNoticeFor(replacement string) string {
	return Localizef(
		"DEPRECATED: this command is deprecated, use `%s` instead. Removal target: v0.15.",
		"DEPRECATED: このコマンドは非推奨です。代わりに `%s` を使用してください。削除予定: v0.15。",
		replacement,
	)
}
