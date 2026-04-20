package usecase

import "regexp"

const (
	redactedAuditValue      = "[REDACTED]"
	redactedPrivateKeyValue = "[REDACTED PRIVATE KEY]"
)

var auditPayloadRedactors = []auditPayloadRedactor{
	{
		pattern:     regexp.MustCompile(`(?s)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`),
		replacement: redactedPrivateKeyValue,
	},
	{
		pattern:     regexp.MustCompile(`(?im)((?:authorization|x-api-key|x-auth-token|cookie|set-cookie)\s*:\s*)([^\r\n]+)`),
		replacement: `${1}` + redactedAuditValue,
	},
	{
		pattern:     regexp.MustCompile(`(?im)((?:authorization)\s*=\s*bearer\s+)([^\s"']+)`),
		replacement: `${1}` + redactedAuditValue,
	},
	{
		pattern:     regexp.MustCompile(`(?i)("(?:access_token|refresh_token|id_token|token|api[_-]?key|secret|client_secret|password|passwd|session[_-]?token)"\s*:\s*")([^"]*)(")`),
		replacement: `${1}` + redactedAuditValue + `${3}`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)('(?:access_token|refresh_token|id_token|token|api[_-]?key|secret|client_secret|password|passwd|session[_-]?token)'\s*:\s*')([^']*)(')`),
		replacement: `${1}` + redactedAuditValue + `${3}`,
	},
	{
		pattern:     regexp.MustCompile(`(?im)((?:^|[\s])(?:export\s+)?(?:access_token|refresh_token|id_token|token|api[_-]?key|secret|client_secret|password|passwd|session[_-]?token)=")([^"]*)(")`),
		replacement: `${1}` + redactedAuditValue + `${3}`,
	},
	{
		pattern:     regexp.MustCompile(`(?im)((?:^|[\s])(?:export\s+)?(?:access_token|refresh_token|id_token|token|api[_-]?key|secret|client_secret|password|passwd|session[_-]?token)=')([^']*)(')`),
		replacement: `${1}` + redactedAuditValue + `${3}`,
	},
	{
		pattern:     regexp.MustCompile(`(?im)((?:^|[\s])(?:export\s+)?(?:access_token|refresh_token|id_token|token|api[_-]?key|secret|client_secret|password|passwd|session[_-]?token)=)([^\s"']+)`),
		replacement: `${1}` + redactedAuditValue,
	},
	{
		pattern:     regexp.MustCompile(`(?i)([?&](?:access_token|refresh_token|id_token|token|api[_-]?key|secret|client_secret|password|passwd|session[_-]?token)=)([^&\s]+)`),
		replacement: `${1}` + redactedAuditValue,
	},
}

type auditPayloadRedactor struct {
	pattern     *regexp.Regexp
	replacement string
}

// RedactWellKnownSecrets applies only the built-in audit redactors
// (private keys, auth headers, token / secret / password shaped
// assignments) to a free-form text payload. It does not consult any
// user-configured `extra_redact_patterns`, which is intentional for
// callers — like transcript capture — that run outside the usecase's
// TracearyConfig plumbing. Returns the sanitized string; whether any
// redaction fired is not surfaced because callers currently treat
// redaction as silent best-effort.
func RedactWellKnownSecrets(value string) string {
	sanitized, _ := redactAuditPayload(value, nil)
	return sanitized
}

func redactAuditPayload(value string, extraRedactors []auditPayloadRedactor) (string, bool) {
	redacted := false
	normalizedValue := value

	for _, redactor := range auditPayloadRedactors {
		updatedValue := redactor.pattern.ReplaceAllString(normalizedValue, redactor.replacement)
		if updatedValue != normalizedValue {
			redacted = true
			normalizedValue = updatedValue
		}
	}

	for _, redactor := range extraRedactors {
		updatedValue := redactor.pattern.ReplaceAllString(normalizedValue, redactor.replacement)
		if updatedValue != normalizedValue {
			redacted = true
			normalizedValue = updatedValue
		}
	}

	return normalizedValue, redacted
}
