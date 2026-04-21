// Package redaction provides shared audit-payload redaction helpers
// that can be consumed both by application/usecase implementations and
// by presentation-layer callers (e.g. the transcript hook) without the
// presentation layer having to import usecase-impl packages.
//
// The built-in redactor set masks private keys, auth headers, and
// common token / secret / password assignment shapes. Callers that
// wire user-supplied extra_redact_patterns should compile them via
// CompileExtraPatterns and pass the result to Apply.
package redaction

import (
	"regexp"
	"strings"

	"golang.org/x/xerrors"
)

const (
	redactedAuditValue      = "[REDACTED]"
	redactedPrivateKeyValue = "[REDACTED PRIVATE KEY]"
)

// Redactor is a single redaction rule: a regex pattern and its
// replacement template.
type Redactor struct {
	Pattern     *regexp.Regexp
	Replacement string
}

var builtinRedactors = []Redactor{
	{
		Pattern:     regexp.MustCompile(`(?s)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`),
		Replacement: redactedPrivateKeyValue,
	},
	{
		Pattern:     regexp.MustCompile(`(?im)((?:authorization|x-api-key|x-auth-token|cookie|set-cookie)\s*:\s*)([^\r\n]+)`),
		Replacement: `${1}` + redactedAuditValue,
	},
	{
		Pattern:     regexp.MustCompile(`(?im)((?:authorization)\s*=\s*bearer\s+)([^\s"']+)`),
		Replacement: `${1}` + redactedAuditValue,
	},
	{
		Pattern:     regexp.MustCompile(`(?i)("(?:access_token|refresh_token|id_token|token|api[_-]?key|secret|client_secret|password|passwd|session[_-]?token)"\s*:\s*")([^"]*)(")`),
		Replacement: `${1}` + redactedAuditValue + `${3}`,
	},
	{
		Pattern:     regexp.MustCompile(`(?i)('(?:access_token|refresh_token|id_token|token|api[_-]?key|secret|client_secret|password|passwd|session[_-]?token)'\s*:\s*')([^']*)(')`),
		Replacement: `${1}` + redactedAuditValue + `${3}`,
	},
	{
		Pattern:     regexp.MustCompile(`(?im)((?:^|[\s])(?:export\s+)?(?:access_token|refresh_token|id_token|token|api[_-]?key|secret|client_secret|password|passwd|session[_-]?token)=")([^"]*)(")`),
		Replacement: `${1}` + redactedAuditValue + `${3}`,
	},
	{
		Pattern:     regexp.MustCompile(`(?im)((?:^|[\s])(?:export\s+)?(?:access_token|refresh_token|id_token|token|api[_-]?key|secret|client_secret|password|passwd|session[_-]?token)=')([^']*)(')`),
		Replacement: `${1}` + redactedAuditValue + `${3}`,
	},
	{
		Pattern:     regexp.MustCompile(`(?im)((?:^|[\s])(?:export\s+)?(?:access_token|refresh_token|id_token|token|api[_-]?key|secret|client_secret|password|passwd|session[_-]?token)=)([^\s"']+)`),
		Replacement: `${1}` + redactedAuditValue,
	},
	{
		Pattern:     regexp.MustCompile(`(?i)([?&](?:access_token|refresh_token|id_token|token|api[_-]?key|secret|client_secret|password|passwd|session[_-]?token)=)([^&\s]+)`),
		Replacement: `${1}` + redactedAuditValue,
	},
}

// BuiltinRedactors returns a defensive copy of the default redactor
// set. Callers may safely prepend or append without mutating package
// state.
func BuiltinRedactors() []Redactor {
	out := make([]Redactor, len(builtinRedactors))
	copy(out, builtinRedactors)
	return out
}

// Apply runs the built-in redactors followed by the caller-supplied
// extras against value and reports whether any substitution fired.
func Apply(value string, extra []Redactor) (string, bool) {
	redacted := false
	normalized := value
	for _, r := range builtinRedactors {
		updated := r.Pattern.ReplaceAllString(normalized, r.Replacement)
		if updated != normalized {
			redacted = true
			normalized = updated
		}
	}
	for _, r := range extra {
		updated := r.Pattern.ReplaceAllString(normalized, r.Replacement)
		if updated != normalized {
			redacted = true
			normalized = updated
		}
	}
	return normalized, redacted
}

// ApplyBuiltin is a convenience for callers that only want the
// built-in redactors and do not care whether anything fired.
func ApplyBuiltin(value string) string {
	normalized, _ := Apply(value, nil)
	return normalized
}

// CompileExtraPatterns compiles caller-supplied regex source strings
// into Redactor entries that Apply can consume. Empty / whitespace-
// only patterns are skipped silently; invalid patterns return an
// error naming the offending source string.
func CompileExtraPatterns(patterns []string) ([]Redactor, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	out := make([]Redactor, 0, len(patterns))
	for _, raw := range patterns {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		compiled, err := regexp.Compile(trimmed)
		if err != nil {
			return nil, xerrors.Errorf("invalid redaction pattern %q: %w", trimmed, err)
		}
		out = append(out, Redactor{
			Pattern:     compiled,
			Replacement: redactedAuditValue,
		})
	}
	return out, nil
}
