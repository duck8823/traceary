package cli

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const (
	retryLoopScanLimit      = 200
	retryLoopMinCount       = 3
	retryLoopSampleIDs      = 3
	retryLoopWindowMax      = 2 * time.Hour
	retryLoopCheckName      = "retry-loops"
)

// retryLoopInput is one failed command_executed row used by the pure classifier.
type retryLoopInput struct {
	EventID   string
	Workspace string
	Agent     string
	Client    string
	Command   string
	Output    string
	ExitCode  string
	CreatedAt time.Time
}

// retryLoopGroup is a conservative cluster of identical failed attempts.
type retryLoopGroup struct {
	Key          string
	Workspace    string
	Agent        string
	Command      string
	ErrorClass   string
	Count        int
	FirstAt      time.Time
	LastAt       time.Time
	SampleIDs    []string
	Preflight    string
}

var (
	retryLoopEISDIRPattern = regexp.MustCompile(`(?i)\b(eisdir|is a directory)\b`)
	retryLoopMissingPattern = regexp.MustCompile(`(?i)\b(enoent|no such file|not found|does not exist)\b`)
	retryLoopOversizedPattern = regexp.MustCompile(`(?i)\b(file (is )?too large|exceeds?.*(token|size|byte)|oversized)\b`)
	retryLoopSandboxPattern = regexp.MustCompile(`(?i)\b(bypasssandbox|sandbox|outside the workspace|requires access to files outside)\b`)
)

// classifyRetryLoopErrorClass maps audit output/command into a stable error class.
func classifyRetryLoopErrorClass(command, output, exitCode string) string {
	blob := command + "\n" + output
	switch {
	case retryLoopEISDIRPattern.MatchString(blob):
		return "eisdir"
	case retryLoopMissingPattern.MatchString(blob):
		return "missing_path"
	case retryLoopOversizedPattern.MatchString(blob):
		return "oversized_file"
	case retryLoopSandboxPattern.MatchString(blob):
		return "sandbox_bypass_required"
	case strings.TrimSpace(exitCode) != "" && strings.TrimSpace(exitCode) != "0":
		return "exit_" + strings.TrimSpace(exitCode)
	default:
		return "failed"
	}
}

// normalizeRetryLoopCommand collapses whitespace and lowercases the first token
// so minor formatting differences do not split the same failed tool call.
func normalizeRetryLoopCommand(command string) string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return ""
	}
	// Host tool envelopes often look like `Read path` or shell lines.
	return strings.Join(fields, " ")
}

func retryLoopGroupKey(in retryLoopInput, errorClass string) string {
	return strings.Join([]string{
		strings.TrimSpace(in.Workspace),
		strings.TrimSpace(in.Agent),
		normalizeRetryLoopCommand(in.Command),
		errorClass,
	}, "\x1f")
}

func retryLoopPreflight(errorClass, command string) string {
	switch errorClass {
	case "eisdir":
		return "verify the path is a file (not a directory) before Read/Edit"
	case "missing_path":
		return "confirm the path exists (ls/stat) before retrying the same tool call"
	case "oversized_file":
		return "read a bounded range or use shell head/tail instead of full-file Read"
	case "sandbox_bypass_required":
		return "request sandbox bypass once, or run the command outside the sandbox host"
	default:
		if cmd := normalizeRetryLoopCommand(command); cmd != "" {
			return "inspect sample event IDs before re-running: " + cmd
		}
		return "inspect sample event IDs before re-running the same failed command"
	}
}

// detectRetryLoops groups failed command audits and returns clusters that look
// like wasteful tool-use loops. Legitimate spaced-out verification re-runs are
// reduced by requiring min count within retryLoopWindowMax of the first hit.
func detectRetryLoops(inputs []retryLoopInput, now time.Time) []retryLoopGroup {
	type acc struct {
		group retryLoopGroup
	}
	byKey := map[string]*acc{}
	for _, in := range inputs {
		if strings.TrimSpace(in.Command) == "" {
			continue
		}
		errorClass := classifyRetryLoopErrorClass(in.Command, in.Output, in.ExitCode)
		key := retryLoopGroupKey(in, errorClass)
		entry, ok := byKey[key]
		if !ok {
			byKey[key] = &acc{group: retryLoopGroup{
				Key:        key,
				Workspace:  strings.TrimSpace(in.Workspace),
				Agent:      strings.TrimSpace(in.Agent),
				Command:    normalizeRetryLoopCommand(in.Command),
				ErrorClass: errorClass,
				Count:      1,
				FirstAt:    in.CreatedAt,
				LastAt:     in.CreatedAt,
				SampleIDs:  []string{in.EventID},
				Preflight:  retryLoopPreflight(errorClass, in.Command),
			}}
			continue
		}
		// Only grow the cluster while events stay inside the window from first.
		if !in.CreatedAt.IsZero() && !entry.group.FirstAt.IsZero() && in.CreatedAt.Sub(entry.group.FirstAt) > retryLoopWindowMax {
			continue
		}
		entry.group.Count++
		if in.CreatedAt.Before(entry.group.FirstAt) || entry.group.FirstAt.IsZero() {
			entry.group.FirstAt = in.CreatedAt
		}
		if in.CreatedAt.After(entry.group.LastAt) {
			entry.group.LastAt = in.CreatedAt
		}
		if len(entry.group.SampleIDs) < retryLoopSampleIDs && in.EventID != "" {
			entry.group.SampleIDs = append(entry.group.SampleIDs, in.EventID)
		}
	}

	groups := make([]retryLoopGroup, 0)
	for _, entry := range byKey {
		if entry.group.Count < retryLoopMinCount {
			continue
		}
		// Prefer recent activity: ignore clusters whose last hit is older than the window.
		if !now.IsZero() && !entry.group.LastAt.IsZero() && now.Sub(entry.group.LastAt) > retryLoopWindowMax {
			continue
		}
		groups = append(groups, entry.group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Key < groups[j].Key
	})
	return groups
}

func (c *RootCLI) inspectRetryLoops(ctx context.Context) doctorCheck {
	if c.event == nil {
		return doctorCheck{
			Name:    retryLoopCheckName,
			Status:  doctorStatusSkip,
			Message: localizef("event usecase is not configured", "event usecase が設定されていません"),
		}
	}
	events, err := c.event.List(ctx, apptypes.NewEventListCriteriaBuilder(retryLoopScanLimit).
		Kind(types.EventKindCommandExecuted).
		FailuresOnly(true).
		Build())
	if err != nil {
		return doctorCheck{
			Name:    retryLoopCheckName,
			Status:  doctorStatusFail,
			Message: localizef("failed to list recent failed command audits: %v", "recent failed command audit の取得に失敗しました: %v", err),
		}
	}

	inputs := make([]retryLoopInput, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		detail, err := c.event.Show(ctx, event.EventID())
		if err != nil {
			return doctorCheck{
				Name:    retryLoopCheckName,
				Status:  doctorStatusFail,
				Message: localizef("failed to inspect failed command audit %s: %v", "failed command audit %s の検査に失敗しました: %v", event.EventID(), err),
			}
		}
		command := strings.TrimSpace(event.Body())
		output := ""
		exitCode := ""
		if audit, ok := detail.CommandAudit().Value(); ok && audit != nil {
			if cmd := strings.TrimSpace(audit.Command()); cmd != "" {
				command = cmd
			}
			output = audit.Output()
			if code, ok := audit.ExitCode().Value(); ok {
				exitCode = fmt.Sprintf("%d", code)
			}
		}
		// Prefer tool-shaped first line for host envelopes embedded in body.
		if command == "" {
			command = firstLine(event.Body())
		}
		inputs = append(inputs, retryLoopInput{
			EventID:   event.EventID().String(),
			Workspace: event.Workspace().String(),
			Agent:     event.Agent().String(),
			Client:    event.Client().String(),
			Command:   command,
			Output:    output,
			ExitCode:  exitCode,
			CreatedAt: event.CreatedAt(),
		})
	}

	groups := detectRetryLoops(inputs, time.Now().UTC())
	if len(groups) == 0 {
		return doctorCheck{
			Name:   retryLoopCheckName,
			Status: doctorStatusPass,
			Message: localizef(
				"scanned %d recent failed command audit(s); no likely retry loops (min_count=%d, window=%s)",
				"%d 件の recent failed command audit を検査しました。likely retry loop はありません (min_count=%d, window=%s)",
				len(inputs), retryLoopMinCount, retryLoopWindowMax,
			),
		}
	}

	samples := make([]string, 0, min(3, len(groups)))
	for i, group := range groups {
		if i >= 3 {
			break
		}
		samples = append(samples, fmt.Sprintf(
			"{class=%s count=%d command=%q first=%s last=%s ids=[%s] preflight=%q}",
			group.ErrorClass,
			group.Count,
			truncateForDoctor(group.Command, 80),
			formatJSONTime(group.FirstAt),
			formatJSONTime(group.LastAt),
			strings.Join(group.SampleIDs, ","),
			group.Preflight,
		))
	}
	return doctorCheck{
		Name:   retryLoopCheckName,
		Status: doctorStatusWarn,
		Hint: Localize(
			"These are likely tool-use retry loops, not proof every repeated command is wrong. Inspect sample event IDs with `traceary show <event_id>` before changing workflow.",
			"これは likely な tool-use retry loop の検出であり、繰り返したコマンドが常に誤りであることの証明ではありません。workflow を変える前に sample event ID を `traceary show <event_id>` で確認してください。",
		),
		Message: localizef(
			"scanned %d recent failed command audit(s); likely_retry_loops=%d samples: %s",
			"%d 件の recent failed command audit を検査しました。likely_retry_loops=%d samples: %s",
			len(inputs), len(groups), strings.Join(samples, " "),
		),
	}
}

func truncateForDoctor(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if limit <= 0 || len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(value, "\n")
	return strings.TrimSpace(line)
}
