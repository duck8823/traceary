package usecase

import (
	"fmt"
	"regexp"
	"strings"

	apptypes "github.com/duck8823/traceary/application/types"
)

// importStubMarker* describe the v0.13 host-context stub markers used by
// the Claude/Gemini activation strategy. The stub uses a different marker
// family from the v0.12 traceary-memories block so a CLAUDE.md /
// GEMINI.md that imports the external memory file is never confused with
// a file that *is* an external memory file.
const (
	importStubMarkerBegin    = "<!-- traceary-memory-import:begin:v1 -->"
	importStubMarkerEnd      = "<!-- traceary-memory-import:end -->"
	importStubCurrentVersion = 1
)

// importStubWarningTemplate is the canonical DO NOT EDIT comment Traceary
// renders inside the host-context import stub. The %s is the host name
// (claude/gemini) so the operator's remediation command points at the
// right activation target.
const importStubWarningTemplate = "<!-- DO NOT EDIT: this import is managed by Traceary. Run `traceary memory activate --target %s --dry-run --diff` before applying updates. -->"

// importStubBeginPattern matches every begin marker version so the
// parser recognises (and refuses to overwrite) a future v2 stub.
var importStubBeginPattern = regexp.MustCompile(`^<!-- traceary-memory-import:begin:v(\d+) -->$`)

// importStubBlockMarkers reuses the v0.12 managed-block parser with the
// stub-specific marker contract. Orphan-begin, duplicate-begin/end, and
// newer-version rejections are inherited from managedBlockMarkers, so
// the stub region behaves identically to the existing memory bridge
// block under the same edge cases.
var importStubBlockMarkers = managedBlockMarkers{
	Begin:          importStubMarkerBegin,
	End:            importStubMarkerEnd,
	BeginPattern:   importStubBeginPattern,
	CurrentVersion: importStubCurrentVersion,
}

// renderImportStubBlock produces the canonical four-line markdown stub
// Traceary writes into the host context file (CLAUDE.md / GEMINI.md).
// The trailing newline lets appendManagedBlockWithSpacing keep one blank
// line between the stub and any user-authored content that follows it.
//
// importPath is rendered verbatim under `@`, so the host-specific
// activation target resolver in #892/#894 controls the relative-vs-
// absolute decision. The default Claude/Gemini layout passes
// `./.traceary/memories/<host>.md`.
func renderImportStubBlock(target apptypes.MemoryBridgeTarget, importPath string) string {
	var body strings.Builder
	body.WriteString(importStubMarkerBegin)
	body.WriteString("\n")
	fmt.Fprintf(&body, importStubWarningTemplate, target.String())
	body.WriteString("\n")
	body.WriteString("@")
	body.WriteString(importPath)
	body.WriteString("\n")
	body.WriteString(importStubMarkerEnd)
	body.WriteString("\n")
	return body.String()
}
