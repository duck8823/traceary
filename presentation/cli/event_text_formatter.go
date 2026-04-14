package cli

import (
	"path"
	"strings"
	"time"

	"github.com/duck8823/traceary/domain/model"
)

const (
	eventWideTimestampLayout = "2006-01-02T15:04:05Z07:00"
	eventCompactTimeLayout   = "15:04:05"

	eventCompactTargetWidth       = 100
	eventCompactSessionIDLen      = 8
	eventCompactWorkspaceMaxRunes = 24
	eventCompactMessageMinRunes   = 16
)

// eventTextFormatOptions controls text-mode event rendering (wide/compact).
// It is shared by tail/list/search so they stay in sync.
type eventTextFormatOptions struct {
	// wide selects the tab-separated 7-column format. When false, compact
	// single-line format is used.
	wide bool
	// utc forces UTC timestamps. When false, the location field (or
	// time.Local if nil) is applied.
	utc bool
	// location is the time.Location used when utc is false. nil means
	// time.Local. Tests should inject time.FixedZone to stay deterministic.
	location *time.Location
}

func (o eventTextFormatOptions) resolvedLocation() *time.Location {
	if o.utc {
		return time.UTC
	}
	if o.location != nil {
		return o.location
	}
	return time.Local
}

func formatTextTimestamp(ts time.Time, opts eventTextFormatOptions, layout string) string {
	return ts.In(opts.resolvedLocation()).Format(layout)
}

func formatEventWideHeader() string {
	return "CREATED_AT\tKIND\tCLIENT\tAGENT\tSESSION_ID\tWORKSPACE\tMESSAGE"
}

func formatEventWideRow(event *model.Event, opts eventTextFormatOptions) string {
	return strings.Join([]string{
		formatTextTimestamp(event.CreatedAt(), opts, eventWideTimestampLayout),
		string(event.Kind()),
		formatOptionalColumn(event.Client().String()),
		string(event.Agent()),
		event.SessionID().String(),
		formatOptionalColumn(event.Workspace().String()),
		truncateMessage(event.Body()),
	}, "\t")
}

func formatEventCompactRow(event *model.Event, opts eventTextFormatOptions) string {
	ts := formatTextTimestamp(event.CreatedAt(), opts, eventCompactTimeLayout)
	kind := string(event.Kind())
	sess := "sess=" + compactSessionID(event.SessionID().String())
	ws := "ws=" + compactWorkspace(event.Workspace().String())

	const sep = "  "
	prefix := ts + sep + kind + sep + sess + sep + ws + sep
	remaining := eventCompactTargetWidth - runeLen(prefix)
	if remaining < eventCompactMessageMinRunes {
		// Workspace may be unusually long; shrink it and recompute.
		trimmedWS := "ws=" + truncateNormalized(compactWorkspace(event.Workspace().String()), eventCompactWorkspaceMaxRunes)
		prefix = ts + sep + kind + sep + sess + sep + trimmedWS + sep
		remaining = eventCompactTargetWidth - runeLen(prefix)
		if remaining < eventCompactMessageMinRunes {
			remaining = eventCompactMessageMinRunes
		}
	}
	msg := truncateNormalized(event.Body(), remaining)
	return prefix + msg
}

// compactSessionID returns the first 8 runes of the session ID. It is meant
// for human-scanning only; callers that need a machine-stable identifier must
// use --wide, --utc or --json.
func compactSessionID(full string) string {
	if full == "" {
		return "-"
	}
	runes := []rune(full)
	if len(runes) <= eventCompactSessionIDLen {
		return string(runes)
	}
	return string(runes[:eventCompactSessionIDLen])
}

// compactWorkspace extracts a basename-like token from a workspace value
// without touching the filesystem. It handles both filesystem paths
// ("/Users/foo/traceary" → "traceary") and owner/repo slugs
// ("duck8823/traceary" → "traceary"). Empty values become "-".
func compactWorkspace(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "-"
	}
	// Normalize Windows-style separators to forward slashes before
	// delegating to path.Base so slug-style ("owner/repo") values get the
	// trailing segment regardless of host OS.
	normalized := strings.ReplaceAll(trimmed, "\\", "/")
	normalized = strings.TrimRight(normalized, "/")
	if normalized == "" {
		return "-"
	}
	base := path.Base(normalized)
	if base == "." || base == "/" {
		return trimmed
	}
	return base
}

// truncateNormalized collapses whitespace runs and caps the result at
// maxRunes runes, appending a single-char ellipsis when truncation happens.
func truncateNormalized(value string, maxRunes int) string {
	normalized := normalizeTabularColumn(value)
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(normalized)
	if len(runes) <= maxRunes {
		return normalized
	}
	if maxRunes == 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}

func runeLen(s string) int {
	return len([]rune(s))
}
