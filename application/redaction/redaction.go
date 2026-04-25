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
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/xerrors"
)

const (
	redactedAuditValue      = "[REDACTED]"
	redactedPrivateKeyValue = "[REDACTED PRIVATE KEY]"
)

// RuleConfig is the configuration-facing structured redaction rule shape.
// Type may be "regex", "field", "url", or "context". When Type is empty,
// a pattern implies regex, fields / paths imply field, and query_params
// implies url. Replacement defaults to [REDACTED]. Targets are optional
// dotted pipeline names such as audit.input or audit.output; omitted targets
// mean the rule applies everywhere.
type RuleConfig struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Pattern     string   `json:"pattern"`
	Replacement string   `json:"replacement"`
	Targets     []string `json:"targets"`
	Fields      []string `json:"fields"`
	Paths       []string `json:"paths"`
	QueryParams []string `json:"query_params"`
	MinLength   int      `json:"min_length"`
}

// Redactor is a single redaction rule: a regex pattern and its
// replacement template.
type Redactor struct {
	Pattern     *regexp.Regexp
	Replacement string
	Name        string
	Targets     []string
}

type structuredRule struct {
	name        string
	typeName    string
	replacement string
	targets     []string
	fields      map[string]struct{}
	paths       map[string]struct{}
	queryParams map[string]struct{}
	minLength   int
}

// Rules contains compiled structured redaction rules. RegexRules includes
// legacy extra_patterns-compatible regex entries plus regex typed rules.
type Rules struct {
	RegexRules      []Redactor
	StructuredRules []structuredRule
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

var builtinStructuredRules = []structuredRule{
	{
		typeName:    "url",
		replacement: redactedAuditValue,
		queryParams: stringSet([]string{"access_token", "refresh_token", "id_token", "token", "api_key", "apikey", "secret", "client_secret", "password", "passwd", "session_token"}),
	},
	{
		typeName:    "context",
		replacement: redactedAuditValue,
		fields:      stringSet([]string{"access_token", "refresh_token", "id_token", "token", "api_key", "apikey", "api-key", "secret", "client_secret", "password", "passwd", "session_token", "authorization", "credential", "credentials"}),
		minLength:   24,
	},
}

var (
	urlLikePattern        = regexp.MustCompile(`https?://[^\s<>'")]+`)
	jwtShapePattern       = regexp.MustCompile(`^[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}$`)
	hexSecretShapePattern = regexp.MustCompile(`^[A-Fa-f0-9]+$`)
	base64ShapePattern    = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)
)

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
	return apply(value, Rules{RegexRules: extra}, "", false)
}

// ApplyWithRules runs built-ins plus structured rules against value for the
// optional pipeline target (for example audit.input or audit.output).
func ApplyWithRules(value string, rules Rules, target string) (string, bool) {
	return apply(value, rules, target, true)
}

func apply(value string, rules Rules, target string, includeStructuredBuiltins bool) (string, bool) {
	redacted := false
	normalized := value
	for _, r := range builtinRedactors {
		updated := r.Pattern.ReplaceAllString(normalized, r.Replacement)
		if updated != normalized {
			redacted = true
			normalized = updated
		}
	}
	structuredRules := rules.StructuredRules
	if includeStructuredBuiltins {
		structuredRules = append(slices.Clone(builtinStructuredRules), structuredRules...)
	}
	for _, r := range structuredRules {
		if !matchesTarget(r.targets, target) {
			continue
		}
		updated := applyStructuredRule(normalized, r)
		if updated != normalized {
			redacted = true
			normalized = updated
		}
	}
	for _, r := range rules.RegexRules {
		if !matchesTarget(r.Targets, target) {
			continue
		}
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

// CompileRules compiles legacy extra_patterns and structured rules into the
// runtime rule set. extra_patterns are appended as all-target regex rules with
// the default replacement, preserving the previous behavior.
func CompileRules(patterns []string, configs []RuleConfig) (Rules, error) {
	regexRules, err := CompileExtraPatterns(patterns)
	if err != nil {
		return Rules{}, err
	}
	compiled := Rules{RegexRules: regexRules}
	for _, cfg := range configs {
		ruleType := inferRuleType(cfg)
		replacement := strings.TrimSpace(cfg.Replacement)
		if replacement == "" {
			replacement = redactedAuditValue
		}
		switch ruleType {
		case "regex":
			trimmed := strings.TrimSpace(cfg.Pattern)
			if trimmed == "" {
				return Rules{}, xerrors.Errorf("redaction rule %q is missing pattern", cfg.Name)
			}
			compiledPattern, err := regexp.Compile(trimmed)
			if err != nil {
				return Rules{}, xerrors.Errorf("invalid redaction rule %q pattern %q: %w", cfg.Name, trimmed, err)
			}
			compiled.RegexRules = append(compiled.RegexRules, Redactor{Pattern: compiledPattern, Replacement: replacement, Name: cfg.Name, Targets: cloneTrimmed(cfg.Targets)})
		case "field":
			fields := stringSet(cfg.Fields)
			paths := stringSet(cfg.Paths)
			if len(fields) == 0 && len(paths) == 0 {
				return Rules{}, xerrors.Errorf("redaction rule %q is missing fields or paths", cfg.Name)
			}
			compiled.StructuredRules = append(compiled.StructuredRules, structuredRule{name: cfg.Name, typeName: ruleType, replacement: replacement, targets: cloneTrimmed(cfg.Targets), fields: fields, paths: paths})
		case "url":
			queryParams := stringSet(cfg.QueryParams)
			if len(queryParams) == 0 {
				queryParams = builtinStructuredRules[0].queryParams
			}
			compiled.StructuredRules = append(compiled.StructuredRules, structuredRule{name: cfg.Name, typeName: ruleType, replacement: replacement, targets: cloneTrimmed(cfg.Targets), queryParams: queryParams})
		case "context":
			fields := stringSet(cfg.Fields)
			if len(fields) == 0 {
				fields = builtinStructuredRules[1].fields
			}
			minLength := cfg.MinLength
			if minLength <= 0 {
				minLength = 24
			}
			compiled.StructuredRules = append(compiled.StructuredRules, structuredRule{name: cfg.Name, typeName: ruleType, replacement: replacement, targets: cloneTrimmed(cfg.Targets), fields: fields, minLength: minLength})
		default:
			return Rules{}, xerrors.Errorf("redaction rule %q has unsupported type %q", cfg.Name, cfg.Type)
		}
	}
	return compiled, nil
}

func inferRuleType(cfg RuleConfig) string {
	if strings.TrimSpace(cfg.Type) != "" {
		return strings.ToLower(strings.TrimSpace(cfg.Type))
	}
	if strings.TrimSpace(cfg.Pattern) != "" {
		return "regex"
	}
	if len(cfg.Fields) > 0 || len(cfg.Paths) > 0 {
		return "field"
	}
	if len(cfg.QueryParams) > 0 {
		return "url"
	}
	return ""
}

func applyStructuredRule(value string, rule structuredRule) string {
	switch rule.typeName {
	case "field":
		return applyJSONRule(value, rule, false)
	case "url":
		return applyURLRule(value, rule)
	case "context":
		return applyJSONRule(value, rule, true)
	default:
		return value
	}
}

func applyJSONRule(value string, rule structuredRule, contextAware bool) string {
	var payload any
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return value
	}
	if decoder.More() {
		return value
	}
	if !jsonDecoderAtEOF(decoder) {
		return value
	}
	changed := redactJSONValue(&payload, nil, rule, contextAware)
	if !changed {
		return value
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return value
	}
	return string(out)
}

func jsonDecoderAtEOF(decoder *json.Decoder) bool {
	var trailing any
	return decoder.Decode(&trailing) == io.EOF
}

func redactJSONValue(value *any, path []string, rule structuredRule, contextAware bool) bool {
	switch typed := (*value).(type) {
	case map[string]any:
		changed := false
		for key, child := range typed {
			childPath := append(slices.Clone(path), key)
			if matchesFieldPath(key, childPath, rule) && (!contextAware || valueHasSecretShape(child, rule.minLength)) {
				typed[key] = rule.replacement
				changed = true
				continue
			}
			if redactJSONValue(&child, childPath, rule, contextAware) {
				typed[key] = child
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for i, child := range typed {
			if redactJSONValue(&child, path, rule, contextAware) {
				typed[i] = child
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func matchesFieldPath(key string, path []string, rule structuredRule) bool {
	if _, ok := rule.fields[strings.ToLower(key)]; ok {
		return true
	}
	_, ok := rule.paths[strings.ToLower(strings.Join(path, "."))]
	return ok
}

func valueHasSecretShape(value any, minLength int) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	trimmed := strings.TrimSpace(text)
	if len(trimmed) < minLength {
		return false
	}
	if jwtShapePattern.MatchString(trimmed) {
		return true
	}
	if len(trimmed) >= minLength && len(trimmed)%2 == 0 && hexSecretShapePattern.MatchString(trimmed) {
		return true
	}
	if len(trimmed) >= minLength && len(trimmed)%4 == 0 && base64ShapePattern.MatchString(trimmed) {
		return true
	}
	return false
}

func applyURLRule(value string, rule structuredRule) string {
	return urlLikePattern.ReplaceAllStringFunc(value, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return raw
		}
		changed := false
		if parsed.User != nil {
			parsed.User = url.User(redactedAuditValue)
			changed = true
		}
		query := parsed.Query()
		for key, values := range query {
			if _, ok := rule.queryParams[strings.ToLower(key)]; !ok {
				continue
			}
			for i := range values {
				values[i] = rule.replacement
			}
			query[key] = values
			changed = true
		}
		if changed {
			parsed.RawQuery = query.Encode()
			return parsed.String()
		}
		return raw
	})
}

func matchesTarget(ruleTargets []string, target string) bool {
	if len(ruleTargets) == 0 {
		return true
	}
	target = strings.ToLower(strings.TrimSpace(target))
	for _, candidate := range ruleTargets {
		if strings.ToLower(strings.TrimSpace(candidate)) == target {
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" {
			continue
		}
		out[trimmed] = struct{}{}
	}
	return out
}

func cloneTrimmed(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// IsZero reports whether the rule set has no regex or structured rules.
func (r Rules) IsZero() bool {
	return len(r.RegexRules) == 0 && len(r.StructuredRules) == 0
}
