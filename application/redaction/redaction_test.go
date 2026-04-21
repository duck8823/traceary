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
