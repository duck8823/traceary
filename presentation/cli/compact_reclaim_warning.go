package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/duck8823/traceary/presentation"
)

func attachCompactReclaimWarning(root *cobra.Command) {
	root.PersistentPostRun = func(cmd *cobra.Command, _ []string) {
		emitCompactReclaimWarning(cmd)
	}
}

func emitCompactReclaimWarning(cmd *cobra.Command) {
	if shouldSkipCompactReclaimWarning(cmd) {
		return
	}
	cfg := presentation.LoadConfig()
	if cfg.Compact.ReclaimWarnBytes <= 0 {
		return
	}
	path, err := resolveDBPath(lookupDBPathFlag(cmd))
	if err != nil || path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.Size() < cfg.Compact.ReclaimWarnBytes {
		return
	}
	_, _ = fmt.Fprintf(
		cmd.ErrOrStderr(),
		"%s\n",
		Localize(
			"TRACEARY: store can reclaim space; run `traceary store compact`",
			"TRACEARY: ストアに回収できる領域があります。`traceary store compact` を実行してください",
		),
	)
}

func shouldSkipCompactReclaimWarning(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		if current.Hidden {
			return true
		}
		switch current.Name() {
		case "hook", "compact", "doctor":
			return true
		}
	}
	return false
}

func lookupDBPathFlag(cmd *cobra.Command) string {
	for current := cmd; current != nil; current = current.Parent() {
		if flag := current.Flags().Lookup("db-path"); flag != nil && flag.Changed {
			return flag.Value.String()
		}
	}
	return ""
}
