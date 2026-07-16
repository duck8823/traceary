// Package sensitivepath classifies command audits for sensitive path/intent
// access. Classification is a separate claim from secret redaction and from
// host capture coverage: matching a path pattern does not prove the file was
// opened unless structured tool evidence supports that.
package sensitivepath

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Class is the sensitivity category that matched.
type Class string

// Known sensitivity classes.
const (
	// ClassNone means no pattern matched.
	ClassNone Class = ""
	// ClassDotenv covers .env style files.
	ClassDotenv Class = "dotenv"
	// ClassSSHKey covers SSH private key paths and basenames.
	ClassSSHKey Class = "ssh_key"
	// ClassCloudCreds covers cloud credential files and directories.
	ClassCloudCreds Class = "cloud_creds"
	// ClassBrowserProfile covers browser profile/cookie paths.
	ClassBrowserProfile Class = "browser_profile"
	// ClassKeyMaterial covers PEM/key files and OS keychains.
	ClassKeyMaterial Class = "key_material"
	// ClassCustom covers user-supplied exact substrings.
	ClassCustom Class = "custom"
)

// Operation is the inferred access operation.
type Operation string

// Known access operations.
const (
	// OpUnknown means the operation could not be inferred.
	OpUnknown Operation = "unknown"
	// OpRead is a read-like operation.
	OpRead Operation = "read"
	// OpWrite is a write-like operation.
	OpWrite Operation = "write"
	// OpStat is a metadata/stat operation.
	OpStat Operation = "stat"
	// OpList is a directory listing operation.
	OpList Operation = "list"
)

// Evidence describes how strong the path-access claim is.
type Evidence string

const (
	// EvidenceCommandTextOnly means only shell/command text matched; file open
	// is not proven.
	EvidenceCommandTextOnly Evidence = "command_text_only"
	// EvidenceStructuredFileTool means a host file tool reported a path.
	EvidenceStructuredFileTool Evidence = "structured_file_tool"
	// EvidenceUnresolvedPath means a pattern matched but the path could not be
	// resolved against a workspace root.
	EvidenceUnresolvedPath Evidence = "unresolved_path"
)

// Coverage is the audit-trail completeness claim for this event.
type Coverage string

// Known coverage levels for the audit payload quality claim.
const (
	// CoverageComplete means command plus I/O material is present and untruncated.
	CoverageComplete Coverage = "complete"
	// CoveragePartial means useful material exists but is incomplete (e.g. truncated).
	CoveragePartial Coverage = "partial"
	// CoverageUnobservable means there is not enough material to judge.
	CoverageUnobservable Coverage = "unobservable"
)

// RedactionClaim is independent from sensitivity matching.
type RedactionClaim string

// Known redaction claim values (separate from sensitivity match).
const (
	// RedactionUnknown means redaction status was not determined.
	RedactionUnknown RedactionClaim = "unknown"
	// RedactionApplied means redaction markers were applied on capture.
	RedactionApplied RedactionClaim = "applied"
	// RedactionUnnecessary means no secret patterns required redaction.
	RedactionUnnecessary RedactionClaim = "unnecessary"
	// RedactionUnavailable means redaction could not run.
	RedactionUnavailable RedactionClaim = "unavailable"
	// RedactionFailed means redaction was attempted and failed.
	RedactionFailed RedactionClaim = "failed"
)

// Classification is the separable sensitive-path claim for one audit event.
type Classification struct {
	Matched        bool           `json:"matched"`
	Class          Class          `json:"class,omitempty"`
	Operation      Operation      `json:"operation,omitempty"`
	Evidence       Evidence       `json:"evidence,omitempty"`
	Coverage       Coverage       `json:"coverage,omitempty"`
	Redaction      RedactionClaim `json:"redaction,omitempty"`
	MatchedPath    string         `json:"matched_path,omitempty"`
	IntentOnly     bool           `json:"intent_only"`
	Summary        string         `json:"summary,omitempty"`
	CoverageGap    string         `json:"coverage_gap,omitempty"`
}

// Input is the raw material used to classify one audit.
type Input struct {
	Command        string
	Input          string
	Output         string
	ToolName       string
	InputTruncated bool
	OutputTruncated bool
	InputRedacted  bool
	OutputRedacted bool
	ExtraPatterns  []string
}

type rule struct {
	class   Class
	pattern *regexp.Regexp
}

var defaultRules = []rule{
	{class: ClassDotenv, pattern: regexp.MustCompile(`(?i)(?:^|[\s"'` + "`" + `=])(\.?env(?:\.[A-Za-z0-9_.-]+)?|\.env)(?:$|[\s"'` + "`" + `])`)},
	{class: ClassDotenv, pattern: regexp.MustCompile(`(?i)\.env(?:\.[A-Za-z0-9_.-]+)?`)},
	{class: ClassSSHKey, pattern: regexp.MustCompile(`(?i)(?:^|[\s"'` + "`" + `])(?:~|/Users/[^/\s]+|/home/[^/\s]+)?/\.ssh(?:/|$)`)},
	{class: ClassSSHKey, pattern: regexp.MustCompile(`(?i)\bid_(?:rsa|ed25519|ecdsa|dsa)\b`)},
	{class: ClassCloudCreds, pattern: regexp.MustCompile(`(?i)(?:^|[\s"'` + "`" + `])(?:~|/Users/[^/\s]+|/home/[^/\s]+)?/\.aws(?:/|$)`)},
	{class: ClassCloudCreds, pattern: regexp.MustCompile(`(?i)(?:^|[\s"'` + "`" + `=])(?:credentials|gcloud/application_default_credentials\.json|service[_-]?account\.json)`)},
	{class: ClassBrowserProfile, pattern: regexp.MustCompile(`(?i)(?:Chrome|Chromium|Firefox|Brave)/.*(?:Default|Profile \d+|Cookies|Login Data)`)},
	{class: ClassBrowserProfile, pattern: regexp.MustCompile(`(?i)Library/Application Support/(?:Google/Chrome|Firefox|BraveSoftware)`)},
	{class: ClassKeyMaterial, pattern: regexp.MustCompile(`(?i)(?:^|[\s"'` + "`" + `])[^\s"'` + "`" + `]+\.(?:pem|p12|pfx|key)(?:$|[\s"'` + "`" + `])`)},
	{class: ClassKeyMaterial, pattern: regexp.MustCompile(`(?i)(?:keychain|login\.keychain-db|Keychains/)`)},
}

// Classify returns the sensitive-path claim for the given audit material.
// Empty or non-matching input yields Matched=false with coverage derived from
// truncation / observability of the payload.
func Classify(in Input) Classification {
	text := strings.Join([]string{in.Command, in.Input, in.Output}, "\n")
	coverage, gap := coverageFrom(in)
	redaction := redactionFrom(in)

	if strings.TrimSpace(text) == "" {
		return Classification{
			Matched:    false,
			Coverage:   CoverageUnobservable,
			Redaction:  redaction,
			CoverageGap: "empty_payload",
			Summary:    "no audit payload to classify",
		}
	}

	matchedClass := ClassNone
	matchedPath := ""
	for _, r := range defaultRules {
		if loc := r.pattern.FindStringIndex(text); loc != nil {
			matchedClass = r.class
			matchedPath = strings.TrimSpace(text[loc[0]:loc[1]])
			break
		}
	}
	if matchedClass == ClassNone {
		for _, raw := range in.ExtraPatterns {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(raw))
			if err != nil {
				continue
			}
			if loc := re.FindStringIndex(text); loc != nil {
				matchedClass = ClassCustom
				matchedPath = strings.TrimSpace(text[loc[0]:loc[1]])
				break
			}
		}
	}

	if matchedClass == ClassNone {
		return Classification{
			Matched:   false,
			Coverage:  coverage,
			Redaction: redaction,
			CoverageGap: gap,
			Summary:   "no sensitive path pattern matched",
		}
	}

	evidence := EvidenceCommandTextOnly
	intentOnly := true
	if isStructuredFileTool(in.ToolName) && looksLikePath(matchedPath) {
		evidence = EvidenceStructuredFileTool
		intentOnly = false
	} else if strings.Contains(matchedPath, "*") || strings.Contains(matchedPath, "?") {
		evidence = EvidenceUnresolvedPath
	}

	op := inferOperation(in.Command, in.ToolName)
	summary := "sensitive path observed via structured file tool"
	if intentOnly {
		summary = "sensitive intent observed; file access not proven"
	}

	return Classification{
		Matched:     true,
		Class:       matchedClass,
		Operation:   op,
		Evidence:    evidence,
		Coverage:    coverage,
		Redaction:   redaction,
		MatchedPath: sanitizeMatchedPath(matchedPath),
		IntentOnly:  intentOnly,
		Summary:     summary,
		CoverageGap: gap,
	}
}

// ClassifyCommandBody classifies a command_executed event body that follows
// Traceary's "command\n\nINPUT:\n...\n\nOUTPUT:\n..." shape.
func ClassifyCommandBody(body string, extra []string) Classification {
	command, input, output := splitCommandBody(body)
	return Classify(Input{
		Command:       command,
		Input:         input,
		Output:        output,
		ExtraPatterns: extra,
	})
}

func splitCommandBody(body string) (command, input, output string) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	parts := strings.SplitN(body, "\n\nINPUT:\n", 2)
	command = strings.TrimSpace(parts[0])
	if len(parts) < 2 {
		return command, "", ""
	}
	rest := parts[1]
	outParts := strings.SplitN(rest, "\n\nOUTPUT:\n", 2)
	input = outParts[0]
	if len(outParts) == 2 {
		output = outParts[1]
	}
	return command, input, output
}

func coverageFrom(in Input) (Coverage, string) {
	switch {
	case in.InputTruncated || in.OutputTruncated:
		return CoveragePartial, "stdout_truncated"
	case strings.TrimSpace(in.Command) == "" && strings.TrimSpace(in.Input) == "" && strings.TrimSpace(in.Output) == "":
		return CoverageUnobservable, "empty_payload"
	case strings.TrimSpace(in.Command) != "" && strings.TrimSpace(in.Input) == "" && strings.TrimSpace(in.Output) == "":
		return CoveragePartial, "command_text_only"
	default:
		return CoverageComplete, ""
	}
}

func redactionFrom(in Input) RedactionClaim {
	switch {
	case in.InputRedacted || in.OutputRedacted:
		return RedactionApplied
	default:
		return RedactionUnknown
	}
}

func inferOperation(command, toolName string) Operation {
	lowerTool := strings.ToLower(strings.TrimSpace(toolName))
	switch lowerTool {
	case "read", "read_file", "cat":
		return OpRead
	case "write", "edit", "search_replace", "apply_patch":
		return OpWrite
	case "list_dir", "ls", "glob":
		return OpList
	case "stat":
		return OpStat
	}

	lower := strings.ToLower(command)
	switch {
	case strings.Contains(lower, "cat ") || strings.HasPrefix(lower, "cat") || strings.Contains(lower, "head ") || strings.Contains(lower, "less ") || strings.Contains(lower, "type "):
		return OpRead
	case strings.Contains(lower, "tee ") || strings.Contains(lower, " >") || strings.Contains(lower, ">>") || strings.Contains(lower, "cp ") || strings.Contains(lower, "mv "):
		return OpWrite
	case strings.Contains(lower, "ls ") || strings.HasPrefix(lower, "ls") || strings.Contains(lower, "find "):
		return OpList
	case strings.Contains(lower, "stat ") || strings.Contains(lower, "test -"):
		return OpStat
	default:
		return OpUnknown
	}
}

func isStructuredFileTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read", "write", "edit", "search_replace", "read_file", "write_file", "list_dir", "glob":
		return true
	default:
		return false
	}
}

func looksLikePath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.Contains(s, "/") || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "~") {
		return true
	}
	return filepath.Ext(s) != ""
}

func sanitizeMatchedPath(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`+"`")
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

// DefaultPatternDescriptions returns bilingual documentation lines for the
// built-in pattern set (for docs generation / help text).
func DefaultPatternDescriptions() []string {
	return []string{
		"dotenv: .env and .env.* files",
		"ssh_key: ~/.ssh and id_rsa / id_ed25519 style key basenames",
		"cloud_creds: ~/.aws, credentials files, service account JSON",
		"browser_profile: Chrome / Firefox / Brave profile and cookie paths",
		"key_material: *.pem / *.key / *.p12 and macOS keychain paths",
		"custom: user-supplied exact substrings via config extra patterns",
	}
}
