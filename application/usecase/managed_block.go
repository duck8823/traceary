package usecase

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

// managedBlockMarkers describes the marker contract Traceary uses to find,
// parse, and replace a managed region inside a host file. Different host
// activation files use different marker prefixes (the v0.13 host-context
// import stub and the external memory file each get their own markers),
// so the parser is parameterized rather than hard-coded to one prefix.
type managedBlockMarkers struct {
	// Begin is the canonical begin line for the current marker version.
	Begin string
	// End is the canonical end line. Marker end lines do not carry a
	// version suffix in the v0.12 contract.
	End string
	// BeginPattern matches every supported begin marker version.
	BeginPattern *regexp.Regexp
	// CurrentVersion is the highest version Traceary will overwrite. A
	// region whose begin marker version exceeds this is rejected.
	CurrentVersion int
}

// memoryBridgeBlockMarkers is the marker contract for the Traceary-managed
// memory block (`<!-- traceary-memories:* -->`). v0.12 ships this single
// contract for Codex; v0.13 reuses the same contract for Claude/Gemini
// external memory files. The activation usecase is the only caller in
// v0.13.0-2; #891 introduces the stub-marker contract for the host
// context import line.
var memoryBridgeBlockMarkers = managedBlockMarkers{
	Begin:          MemoryBridgeMarkerBegin,
	End:            MemoryBridgeMarkerEnd,
	BeginPattern:   memoryBridgeBeginPattern,
	CurrentVersion: MemoryBridgeCurrentVersion,
}

// managedBlockRegion is the byte range a managed region occupies inside
// the host file. The semi-open interval [start, end) covers the begin
// marker line through and including the end marker line (and its
// trailing newline if any), so callers can substitute the entire region
// without manual offset arithmetic.
type managedBlockRegion struct {
	start int
	end   int
}

// findRegion scans content for a Traceary managed region. The boolean is
// false when no begin marker is present. An error is returned for
// malformed or unsupported regions: orphan begin markers, duplicate begin
// or end markers, and begin markers whose version exceeds CurrentVersion.
func (m managedBlockMarkers) findRegion(content string) (managedBlockRegion, bool, error) {
	offset := 0
	beginStart := -1
	endOffset := -1
	for _, line := range splitContentLines(content) {
		trimmed := strings.TrimSpace(line.text)
		if version, ok := matchManagedBeginLine(m.BeginPattern, trimmed); ok {
			if version > m.CurrentVersion {
				return managedBlockRegion{}, false, xerrors.Errorf("refusing to overwrite newer Traceary managed block version v%d (current v%d)", version, m.CurrentVersion)
			}
			if beginStart >= 0 {
				return managedBlockRegion{}, false, xerrors.Errorf("multiple Traceary managed memory blocks found")
			}
			beginStart = offset
		}
		if trimmed == m.End {
			if beginStart < 0 {
				offset += len(line.raw)
				continue
			}
			if endOffset >= 0 {
				return managedBlockRegion{}, false, xerrors.Errorf("multiple Traceary managed memory end markers found")
			}
			endOffset = offset + len(line.raw)
		}
		offset += len(line.raw)
	}
	if beginStart < 0 {
		return managedBlockRegion{}, false, nil
	}
	if endOffset < 0 {
		return managedBlockRegion{}, false, xerrors.Errorf("Traceary managed memory begin marker found without end marker")
	}
	return managedBlockRegion{start: beginStart, end: endOffset}, true, nil
}

// replaceOrAppend computes the merged content and observable apply
// action for a host file managed by these markers. exists indicates
// whether the original file is present; when false the managed block
// becomes the entire content. When the region is found, the existing
// region is replaced; otherwise the block is appended at end-of-file
// using the v0.12 spacing rule (preserve user content, leave one blank
// line before the managed region).
func (m managedBlockMarkers) replaceOrAppend(existing string, exists bool, managedBlock string) (string, apptypes.MemoryActivationApplyAction, error) {
	if !exists {
		return managedBlock, apptypes.MemoryActivationApplyCreated, nil
	}
	region, found, err := m.findRegion(existing)
	if err != nil {
		return "", "", err
	}
	if found {
		merged := existing[:region.start] + managedBlock + existing[region.end:]
		if merged == existing {
			return existing, apptypes.MemoryActivationApplyNoop, nil
		}
		return merged, apptypes.MemoryActivationApplyUpdated, nil
	}
	merged := appendManagedBlockWithSpacing(existing, managedBlock)
	if merged == existing {
		return existing, apptypes.MemoryActivationApplyNoop, nil
	}
	return merged, apptypes.MemoryActivationApplyUpdated, nil
}

func matchManagedBeginLine(pattern *regexp.Regexp, line string) (int, bool) {
	match := pattern.FindStringSubmatch(line)
	if match == nil {
		return 0, false
	}
	parsed, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return parsed, true
}

// appendManagedBlockWithSpacing appends a managed block to existing
// content using the activation-contract spacing rule: leave exactly one
// blank line before the managed region so the user's content and the
// managed region remain visibly separate after a `cat` of the file.
func appendManagedBlockWithSpacing(existing, managedBlock string) string {
	if existing == "" {
		return managedBlock
	}
	if !strings.HasSuffix(existing, "\n") {
		return existing + "\n\n" + managedBlock
	}
	if !strings.HasSuffix(existing, "\n\n") {
		return existing + "\n" + managedBlock
	}
	return existing + managedBlock
}

type contentLine struct {
	raw  string
	text string
}

func splitContentLines(content string) []contentLine {
	if content == "" {
		return nil
	}
	lines := make([]contentLine, 0, strings.Count(content, "\n")+1)
	for len(content) > 0 {
		next := strings.IndexByte(content, '\n')
		if next < 0 {
			lines = append(lines, contentLine{raw: content, text: strings.TrimSuffix(content, "\r")})
			break
		}
		raw := content[:next+1]
		text := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		lines = append(lines, contentLine{raw: raw, text: text})
		content = content[next+1:]
	}
	return lines
}
