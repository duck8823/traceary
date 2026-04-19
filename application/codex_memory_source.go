package application

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// CodexMemorySource reads raw candidate rows out of a Codex memory layout
// (for example ~/.codex/memories/MEMORY.md). It is declared in the
// application package so the usecase can depend on the interface and the
// filesystem adapter can supply the concrete reader without dragging in
// parser-specific types.
type CodexMemorySource interface {
	// Load walks the configured root (criteria.Root) and returns candidate
	// rows. Implementations are expected to perform path containment
	// checks — the returned slice must only contain rows whose source path
	// stays inside the resolved root — and to emit non-fatal issues via
	// the warnings return value so the usecase can forward them to the
	// CLI.
	Load(ctx context.Context, criteria apptypes.CodexImportCriteria) ([]apptypes.ImportedMemoryCandidate, []string, error)
}
