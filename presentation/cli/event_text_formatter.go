package cli

import (
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
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
	// fields is the resolved compact column order. nil means "use the
	// built-in default" so callers that do not care about field selection
	// can stay simple.
	fields []readFieldID
	// colorEnabled wraps compact rows with ANSI escape sequences when true.
	// Wide and JSON writers ignore this flag so their legacy / machine
	// readable contract is preserved.
	colorEnabled bool
}

// compactRowExtras carries hydrated data that a specific field needs but is
// not on the Event aggregate. Callers populate this lazily before rendering.
// Fields left at their zero value render as "-".
type compactRowExtras struct {
	exitCode types.Optional[int]
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
	return "CREATED_AT\tKIND\tCLIENT\tAGENT\tSESSION_ID\tWORKSPACE\tSOURCE_HOOK\tMESSAGE"
}

func formatEventWideRow(event *model.Event, opts eventTextFormatOptions) string {
	return strings.Join([]string{
		formatTextTimestamp(event.CreatedAt(), opts, eventWideTimestampLayout),
		string(event.Kind()),
		formatOptionalColumn(event.Client().String()),
		string(event.Agent()),
		event.SessionID().String(),
		formatOptionalColumn(event.Workspace().String()),
		formatOptionalColumn(event.SourceHook()),
		truncateMessage(apptypes.ExtractPlainBody(event.Body())),
	}, "\t")
}

// formatEventCompactRow renders a compact single-line event row using the
// resolved field list on opts. The built-in default column order preserves
// v0.6.1 byte-for-byte output, including the 100-column width budget that
// shrinks the workspace token when the message has no room. Custom column
// orders fall back to a fixed messageColumnMaxWidth truncation for the
// message field and do not shrink other tokens.
func formatEventCompactRow(event *model.Event, opts eventTextFormatOptions, extras compactRowExtras) string {
	fields := opts.fields
	if len(fields) == 0 {
		fields = defaultReadFields
	}

	const sep = "  "

	messageIndex := -1
	for i, f := range fields {
		if f == readFieldMessage {
			messageIndex = i
			break
		}
	}

	tokens := make([]string, len(fields))
	for i, f := range fields {
		if f == readFieldMessage {
			continue
		}
		tokens[i] = renderCompactToken(event, f, opts, extras)
	}

	if messageIndex == -1 {
		return strings.Join(tokens, sep)
	}

	messageIsLast := messageIndex == len(fields)-1
	if !messageIsLast {
		tokens[messageIndex] = truncateNormalized(apptypes.ExtractPlainBody(event.Body()), messageColumnMaxWidth)
		return strings.Join(tokens, sep)
	}

	prefix := strings.Join(tokens[:messageIndex], sep)
	if messageIndex > 0 {
		prefix += sep
	}
	remaining := eventCompactTargetWidth - runeLen(prefix)
	if remaining < eventCompactMessageMinRunes {
		for i := 0; i < messageIndex; i++ {
			if fields[i] == readFieldWorkspace {
				tokens[i] = "ws=" + truncateNormalized(compactWorkspace(event.Workspace().String()), eventCompactWorkspaceMaxRunes)
				break
			}
		}
		prefix = strings.Join(tokens[:messageIndex], sep)
		if messageIndex > 0 {
			prefix += sep
		}
		remaining = eventCompactTargetWidth - runeLen(prefix)
		if remaining < eventCompactMessageMinRunes {
			remaining = eventCompactMessageMinRunes
		}
	}
	plain := prefix + truncateNormalized(apptypes.ExtractPlainBody(event.Body()), remaining)
	if opts.colorEnabled {
		exitCode, exitCodeSet := extras.exitCode.Value()
		return applyCompactRowHighlight(plain, string(event.Kind()), exitCode, exitCodeSet)
	}
	return plain
}

func renderCompactToken(event *model.Event, id readFieldID, opts eventTextFormatOptions, extras compactRowExtras) string {
	switch id {
	case readFieldTS:
		return formatTextTimestamp(event.CreatedAt(), opts, eventCompactTimeLayout)
	case readFieldKind:
		return string(event.Kind())
	case readFieldSession:
		return "sess=" + compactSessionID(event.SessionID().String())
	case readFieldWorkspace:
		return "ws=" + compactWorkspace(event.Workspace().String())
	case readFieldClient:
		return "client=" + formatOptionalColumn(event.Client().String())
	case readFieldAgent:
		return "agent=" + formatOptionalColumn(event.Agent().String())
	case readFieldExitCode:
		return "exit=" + formatOptionalExitCode(extras.exitCode)
	case readFieldEventID:
		return "id=" + event.EventID().String()
	case readFieldSourceHook:
		return "hook=" + formatOptionalColumn(event.SourceHook())
	}
	return ""
}

func formatOptionalExitCode(value types.Optional[int]) string {
	if code, ok := value.Value(); ok {
		return strconv.Itoa(code)
	}
	return "-"
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
// the given visual column budget, appending a single-char ellipsis when
// truncation happens. The budget is measured in **visual columns**
// (East Asian Wide characters count as 2) so a CJK-heavy field never
// overruns its tabular slot. The parameter is named `maxRunes` for
// historical reasons; treat it as a column width.
func truncateNormalized(value string, maxRunes int) string {
	normalized := normalizeTabularColumn(value)
	if maxRunes <= 0 {
		return ""
	}
	if runewidth.StringWidth(normalized) <= maxRunes {
		return normalized
	}
	if maxRunes == 1 {
		return "…"
	}
	const ellipsis = '…'
	ellipsisWidth := runewidth.RuneWidth(ellipsis)
	if ellipsisWidth > maxRunes {
		// Pathological narrow budget. Fall back to the ellipsis itself.
		return string(ellipsis)
	}
	budget := maxRunes - ellipsisWidth
	width := 0
	cut := 0
	for _, r := range normalized {
		w := runewidth.RuneWidth(r)
		if width+w > budget {
			break
		}
		width += w
		cut += utf8.RuneLen(r)
	}
	return normalized[:cut] + string(ellipsis)
}

func runeLen(s string) int {
	return runewidth.StringWidth(s)
}
