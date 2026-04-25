package types_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/redaction"
	apptypes "github.com/duck8823/traceary/application/types"
)

func TestAuditRedactionBuilder_Defaults(t *testing.T) {
	t.Parallel()

	redaction := apptypes.NewAuditRedactionBuilder().Build()

	if redaction.AllowSecrets() {
		t.Errorf("AllowSecrets() = true, want false")
	}
	if diff := cmp.Diff(0, redaction.MaxInputBytes()); diff != "" {
		t.Errorf("MaxInputBytes() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(0, redaction.MaxOutputBytes()); diff != "" {
		t.Errorf("MaxOutputBytes() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string(nil), redaction.ExtraRedactPatterns()); diff != "" {
		t.Errorf("ExtraRedactPatterns() mismatch (-want +got):\n%s", diff)
	}
}

func TestAuditRedactionBuilder_AllSettersChained(t *testing.T) {
	t.Parallel()

	patterns := []string{`password=\S+`, `token=\S+`}

	redaction := apptypes.NewAuditRedactionBuilder().
		AllowSecrets(true).
		MaxInputBytes(4096).
		MaxOutputBytes(8192).
		ExtraRedactPatterns(patterns).
		Build()

	if !redaction.AllowSecrets() {
		t.Errorf("AllowSecrets() = false, want true")
	}
	if diff := cmp.Diff(4096, redaction.MaxInputBytes()); diff != "" {
		t.Errorf("MaxInputBytes() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(8192, redaction.MaxOutputBytes()); diff != "" {
		t.Errorf("MaxOutputBytes() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(patterns, redaction.ExtraRedactPatterns()); diff != "" {
		t.Errorf("ExtraRedactPatterns() mismatch (-want +got):\n%s", diff)
	}
}

func TestAuditRedaction_ExtraRedactPatternsDefensiveCopy(t *testing.T) {
	t.Parallel()

	original := []string{`password=\S+`, `token=\S+`}

	redaction := apptypes.NewAuditRedactionBuilder().
		ExtraRedactPatterns(original).
		Build()

	// Mutate source slice after build.
	original[0] = "mutated-source"
	if diff := cmp.Diff([]string{`password=\S+`, `token=\S+`}, redaction.ExtraRedactPatterns()); diff != "" {
		t.Errorf("builder did not defensively copy source slice (-want +got):\n%s", diff)
	}

	// Mutate the returned slice from the getter.
	returned := redaction.ExtraRedactPatterns()
	returned[0] = "mutated-return"
	if diff := cmp.Diff([]string{`password=\S+`, `token=\S+`}, redaction.ExtraRedactPatterns()); diff != "" {
		t.Errorf("ExtraRedactPatterns() is not a defensive copy (-want +got):\n%s", diff)
	}
}

func TestAuditRedaction_StructuredRulesDefensiveCopy(t *testing.T) {
	t.Parallel()

	original := []redaction.RuleConfig{{Name: "internal", Pattern: `INT-\w+`}}
	redactionCfg := apptypes.NewAuditRedactionBuilder().
		StructuredRules(original).
		Build()
	original[0].Name = "mutated"

	if got := redactionCfg.StructuredRules(); got[0].Name != "internal" {
		t.Fatalf("StructuredRules() = %v, builder did not copy input", got)
	}
	returned := redactionCfg.StructuredRules()
	returned[0].Name = "mutated"
	if got := redactionCfg.StructuredRules(); got[0].Name != "internal" {
		t.Fatalf("StructuredRules() = %v, getter did not copy output", got)
	}
}
