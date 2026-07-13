package cli

import (
	"strings"
	"testing"
)

func TestBuildGrokDoctorChecks(t *testing.T) {
	healthy := grokDoctorState{CLIAvailable: true, HostVersion: "0.2.99", PluginInstalled: true, PluginEnabled: true, PluginVersion: "0.23.0", ProjectTrusted: true, NativeHooks: true, MCPServers: 1, Skills: 3}
	tests := []struct {
		name       string
		mutate     func(*grokDoctorState)
		check      string
		status     string
		messageSub string
	}{
		{name: "absent CLI", mutate: func(s *grokDoctorState) { *s = grokDoctorState{} }, check: "grok-cli", status: doctorStatusFail, messageSub: "not installed"},
		{name: "absent plugin", mutate: func(s *grokDoctorState) { s.PluginInstalled = false }, check: "grok-plugin", status: doctorStatusWarn, messageSub: "not installed"},
		{name: "version mismatch", mutate: func(s *grokDoctorState) { s.PluginVersion = "0.22.0" }, check: "grok-plugin", status: doctorStatusWarn, messageSub: "does not match"},
		{name: "untrusted project hooks", mutate: func(s *grokDoctorState) { s.ProjectHooks, s.ProjectTrusted = true, false }, check: "grok-hook-trust", status: doctorStatusWarn, messageSub: "not trusted"},
		{name: "missing MCP", mutate: func(s *grokDoctorState) { s.MCPServers = 0 }, check: "grok-mcp", status: doctorStatusWarn, messageSub: "0"},
		{name: "missing skills", mutate: func(s *grokDoctorState) { s.Skills = 2 }, check: "grok-skills", status: doctorStatusWarn, messageSub: "2"},
		{name: "missing hooks", mutate: func(s *grokDoctorState) { s.NativeHooks = false }, check: "grok-hooks", status: doctorStatusWarn, messageSub: "incomplete"},
		{name: "healthy", mutate: func(*grokDoctorState) {}, check: "grok-plugin", status: doctorStatusPass, messageSub: "enabled"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := healthy
			tc.mutate(&state)
			checks := buildGrokDoctorChecks(state, "0.23.0")
			var found *doctorCheck
			for i := range checks {
				if checks[i].Name == tc.check {
					found = &checks[i]
					break
				}
			}
			if found == nil || found.Status != tc.status || !strings.Contains(found.Message, tc.messageSub) {
				t.Fatalf("checks = %+v, want %s %s containing %q", checks, tc.check, tc.status, tc.messageSub)
			}
			for _, check := range checks {
				if strings.Contains(check.Message+check.Hint, "/private/") {
					t.Fatalf("check exposed private path: %+v", check)
				}
			}
		})
	}
}

func TestBuildGrokDoctorChecksSkipsParityForDevelopmentVersion(t *testing.T) {
	state := grokDoctorState{CLIAvailable: true, HostVersion: "0.2.99", PluginInstalled: true, PluginEnabled: true, PluginVersion: "0.22.0", ProjectTrusted: true, NativeHooks: true, MCPServers: 1, Skills: 3}
	checks := buildGrokDoctorChecks(state, "dev (commit=none)")
	for _, check := range checks {
		if check.Name == "grok-plugin" && check.Status != doctorStatusPass {
			t.Fatalf("development version parity check = %+v, want pass", check)
		}
	}
}
