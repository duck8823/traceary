package types

import (
	"slices"

	"github.com/duck8823/traceary/application/redaction"
)

func cloneRuleConfigs(rules []redaction.RuleConfig) []redaction.RuleConfig {
	if rules == nil {
		return nil
	}
	out := make([]redaction.RuleConfig, len(rules))
	for i, rule := range rules {
		out[i] = rule
		out[i].Targets = slices.Clone(rule.Targets)
		out[i].Fields = slices.Clone(rule.Fields)
		out[i].Paths = slices.Clone(rule.Paths)
		out[i].QueryParams = slices.Clone(rule.QueryParams)
	}
	return out
}
