package redaction_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application/redaction"
)

func TestApplyBuiltin_RedactsAuthorizationHeader(t *testing.T) {
	t.Parallel()

	in := "Authorization: Bearer abc.def.ghi"
	got := redaction.ApplyBuiltin(in)
	if strings.Contains(got, "abc.def.ghi") {
		t.Errorf("ApplyBuiltin() = %q, still contains secret token", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("ApplyBuiltin() = %q, expected [REDACTED] placeholder", got)
	}
}

func TestApplyBuiltin_RedactsPrivateKeyBlock(t *testing.T) {
	t.Parallel()

	in := "-----BEGIN RSA PRIVATE KEY-----\nMIIEvQIBADANBgk...\n-----END RSA PRIVATE KEY-----"
	got := redaction.ApplyBuiltin(in)
	if strings.Contains(got, "MIIEvQIBADANBgk") {
		t.Errorf("ApplyBuiltin() leaked private key material: %q", got)
	}
	if !strings.Contains(got, "[REDACTED PRIVATE KEY]") {
		t.Errorf("ApplyBuiltin() = %q, expected [REDACTED PRIVATE KEY] placeholder", got)
	}
}

func TestApply_ReportsWhetherSubstitutionFired(t *testing.T) {
	t.Parallel()

	if _, fired := redaction.Apply("benign text only", nil); fired {
		t.Errorf("Apply() fired = true, expected false for benign input")
	}
	if _, fired := redaction.Apply("password=hunter2", nil); !fired {
		t.Errorf("Apply() fired = false, expected true for secret-shaped input")
	}
}

func TestApply_AppliesExtraRedactors(t *testing.T) {
	t.Parallel()

	extra := []redaction.Redactor{
		{
			Pattern:     regexp.MustCompile(`(?i)my_custom_secret=\S+`),
			Replacement: "[REDACTED]",
		},
	}
	got, fired := redaction.Apply("config: my_custom_secret=s3cr3t", extra)
	if !fired {
		t.Errorf("Apply(extra) fired = false, expected true")
	}
	if strings.Contains(got, "s3cr3t") {
		t.Errorf("Apply(extra) = %q, extra pattern did not redact", got)
	}
}

func TestBuiltinRedactors_ReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()

	first := redaction.BuiltinRedactors()
	if len(first) == 0 {
		t.Fatalf("BuiltinRedactors() = 0 entries, expected non-empty")
	}
	first[0] = redaction.Redactor{}
	second := redaction.BuiltinRedactors()
	if second[0].Pattern == nil {
		t.Errorf("BuiltinRedactors() returned shared slice; mutation leaked across calls")
	}
}

func TestCompileExtraPatterns(t *testing.T) {
	t.Parallel()

	t.Run("empty input returns nil", func(t *testing.T) {
		t.Parallel()
		got, err := redaction.CompileExtraPatterns(nil)
		if err != nil {
			t.Fatalf("CompileExtraPatterns(nil) error = %v", err)
		}
		if got != nil {
			t.Errorf("CompileExtraPatterns(nil) = %v, want nil", got)
		}
	})

	t.Run("trims and skips blank entries", func(t *testing.T) {
		t.Parallel()
		got, err := redaction.CompileExtraPatterns([]string{"", "   ", `foo=\S+`})
		if err != nil {
			t.Fatalf("CompileExtraPatterns() error = %v", err)
		}
		if len(got) != 1 {
			t.Errorf("CompileExtraPatterns() len = %d, want 1", len(got))
		}
	})

	t.Run("reports invalid pattern", func(t *testing.T) {
		t.Parallel()
		_, err := redaction.CompileExtraPatterns([]string{`(unclosed`})
		if err == nil {
			t.Fatalf("CompileExtraPatterns(invalid) error = nil, want error")
		}
		if !strings.Contains(err.Error(), "invalid redaction pattern") {
			t.Errorf("CompileExtraPatterns(invalid) error = %v, expected \"invalid redaction pattern\" phrasing", err)
		}
	})
}

func TestApplyWithRules_FieldRuleRedactsJSONKeysAndPaths(t *testing.T) {
	t.Parallel()

	rules, err := redaction.CompileRules(nil, []redaction.RuleConfig{
		{
			Name:        "sensitive-fields",
			Type:        "field",
			Fields:      []string{"credential"},
			Paths:       []string{"nested.apiKey"},
			Replacement: "[FIELD]",
		},
	})
	if err != nil {
		t.Fatalf("CompileRules() error = %v", err)
	}

	got, fired := redaction.ApplyWithRules(`{"credential":"plain-value","nested":{"apiKey":"abc123"},"safe":"keep"}`, rules, "audit.input")
	if !fired {
		t.Fatalf("ApplyWithRules() fired = false, want true")
	}
	for _, leaked := range []string{"plain-value", "abc123"} {
		if strings.Contains(got, leaked) {
			t.Errorf("ApplyWithRules() = %q, leaked %q", got, leaked)
		}
	}
	if !strings.Contains(got, `"safe":"keep"`) {
		t.Errorf("ApplyWithRules() = %q, expected safe field to remain", got)
	}
}

func TestApplyWithRules_JSONRulePreservesPayloadWithTrailingText(t *testing.T) {
	t.Parallel()

	rules, err := redaction.CompileRules(nil, nil)
	if err != nil {
		t.Fatalf("CompileRules() error = %v", err)
	}

	input := "{\"authorization\":\"0123456789abcdef0123456789abcdef\"}\nnext line\nmore content"
	got, _ := redaction.ApplyWithRules(input, rules, "audit.input")

	if !strings.Contains(got, "next line\nmore content") {
		t.Fatalf("ApplyWithRules() = %q, expected trailing text to be preserved", got)
	}
}

func TestApplyWithRules_URLRuleRedactsUserInfoAndConfiguredQueryParams(t *testing.T) {
	t.Parallel()

	rules, err := redaction.CompileRules(nil, []redaction.RuleConfig{
		{
			Name:        "signed-url",
			Type:        "url",
			QueryParams: []string{"signature"},
			Replacement: "[URL-SECRET]",
		},
	})
	if err != nil {
		t.Fatalf("CompileRules() error = %v", err)
	}

	got, fired := redaction.ApplyWithRules("fetch https://user:pass@example.com/path?signature=abc&keep=1", rules, "audit.input")
	if !fired {
		t.Fatalf("ApplyWithRules() fired = false, want true")
	}
	for _, leaked := range []string{"user:pass", "signature=abc"} {
		if strings.Contains(got, leaked) {
			t.Errorf("ApplyWithRules() = %q, leaked %q", got, leaked)
		}
	}
	if !strings.Contains(got, "keep=1") || !strings.Contains(got, "signature=%5BURL-SECRET%5D") {
		t.Errorf("ApplyWithRules() = %q, expected redacted signature and preserved query", got)
	}
}

func TestApplyWithRules_ContextRuleOnlyRedactsSecretShapesForSensitiveKeys(t *testing.T) {
	t.Parallel()

	rules, err := redaction.CompileRules(nil, []redaction.RuleConfig{
		{
			Name:      "credential-shapes",
			Type:      "context",
			Fields:    []string{"credential"},
			MinLength: 24,
		},
	})
	if err != nil {
		t.Fatalf("CompileRules() error = %v", err)
	}

	got, fired := redaction.ApplyWithRules(`{"credential":"0123456789abcdef0123456789abcdef","note":"0123456789abcdef0123456789abcdef","short":"abc"}`, rules, "audit.input")
	if !fired {
		t.Fatalf("ApplyWithRules() fired = false, want true")
	}
	if strings.Contains(got, `"credential":"0123456789abcdef0123456789abcdef"`) {
		t.Errorf("ApplyWithRules() = %q, expected credential to be redacted", got)
	}
	if !strings.Contains(got, `"note":"0123456789abcdef0123456789abcdef"`) {
		t.Errorf("ApplyWithRules() = %q, expected non-sensitive surrounding key to remain", got)
	}
}

func TestApplyWithRules_RegexRuleSupportsReplacementAndTargetScoping(t *testing.T) {
	t.Parallel()

	rules, err := redaction.CompileRules([]string{`legacy-secret=\S+`}, []redaction.RuleConfig{
		{
			Name:        "input-only",
			Pattern:     `INTERNAL-[A-Z0-9]+`,
			Replacement: "[INTERNAL]",
			Targets:     []string{"audit.input"},
		},
	})
	if err != nil {
		t.Fatalf("CompileRules() error = %v", err)
	}

	input, inputFired := redaction.ApplyWithRules("INTERNAL-ABC legacy-secret=value", rules, "audit.input")
	output, outputFired := redaction.ApplyWithRules("INTERNAL-ABC legacy-secret=value", rules, "audit.output")
	if !inputFired || !outputFired {
		t.Fatalf("ApplyWithRules() fired = (%v, %v), want both true because legacy rule applies everywhere", inputFired, outputFired)
	}
	if !strings.Contains(input, "[INTERNAL]") || strings.Contains(input, "legacy-secret=value") {
		t.Errorf("input ApplyWithRules() = %q", input)
	}
	if !strings.Contains(output, "INTERNAL-ABC") || strings.Contains(output, "legacy-secret=value") {
		t.Errorf("output ApplyWithRules() = %q", output)
	}
}
