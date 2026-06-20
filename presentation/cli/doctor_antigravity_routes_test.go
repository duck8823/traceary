package cli

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestClassifyAntigravityHookFile(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		readErr error
		want    antigravityHookFileHealth
	}{
		{
			name:    "absent when file does not exist",
			readErr: os.ErrNotExist,
			want:    antigravityHookFileAbsent,
		},
		{
			name:    "invalid when read fails for a non-not-exist reason",
			readErr: errors.New("permission denied"),
			want:    antigravityHookFileInvalid,
		},
		{
			name: "healthy when document carries the traceary group",
			data: []byte(healthyAntigravityHooksJSON),
			want: antigravityHookFileHealthy,
		},
		{
			name: "no group when document is a valid object without traceary",
			data: []byte(`{"someOtherGroup": {}}`),
			want: antigravityHookFileNoGroup,
		},
		{
			name: "invalid when document is not a JSON object",
			data: []byte(`["not", "an", "object"]`),
			want: antigravityHookFileInvalid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyAntigravityHookFile(tt.data, tt.readErr); got != tt.want {
				t.Fatalf("classifyAntigravityHookFile = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildAntigravityHookFileCheck(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	const label = "workspace"
	const checkName = "antigravity-hooks-workspace"
	const path = "/proj/.agents/hooks.json"

	tests := []struct {
		name        string
		health      antigravityHookFileHealth
		wantStatus  string
		wantHealthy bool
		wantPresent bool
	}{
		{"healthy passes and is present", antigravityHookFileHealthy, doctorStatusPass, true, true},
		{"no group skips but is present", antigravityHookFileNoGroup, doctorStatusSkip, false, true},
		{"invalid fails and is present", antigravityHookFileInvalid, doctorStatusFail, false, true},
		{"absent skips and is not present", antigravityHookFileAbsent, doctorStatusSkip, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check, healthy, present := buildAntigravityHookFileCheck(label, checkName, path, tt.health)
			if check.Name != checkName {
				t.Fatalf("Name = %q, want %q", check.Name, checkName)
			}
			if check.Status != tt.wantStatus {
				t.Fatalf("Status = %q, want %q", check.Status, tt.wantStatus)
			}
			if healthy != tt.wantHealthy {
				t.Fatalf("healthy = %v, want %v", healthy, tt.wantHealthy)
			}
			if present != tt.wantPresent {
				t.Fatalf("present = %v, want %v", present, tt.wantPresent)
			}
			if !strings.Contains(check.Message, path) {
				t.Fatalf("message should reference the path %q: %q", path, check.Message)
			}
		})
	}
}

// route builders for summary tests.
func healthyRoute(label string) antigravityHookRoute {
	return antigravityHookRoute{Label: label, Healthy: true, Present: true}
}

func absentRoute(label string) antigravityHookRoute {
	return antigravityHookRoute{Label: label, Healthy: false, Present: false}
}

func presentUnhealthyRoute(label string) antigravityHookRoute {
	return antigravityHookRoute{Label: label, Healthy: false, Present: true}
}

func TestAntigravityHookRouteSummary(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")

	tests := []struct {
		name         string
		routes       []antigravityHookRoute
		wantStatus   string
		wantContains []string
	}{
		{
			name: "only workspace installed passes without warning",
			routes: []antigravityHookRoute{
				healthyRoute(antigravityRouteWorkspaceLabel),
				absentRoute(antigravityRouteUserLabel),
				absentRoute(antigravityRoutePluginLabel),
			},
			wantStatus:   doctorStatusPass,
			wantContains: []string{antigravityRouteWorkspaceLabel, "optional"},
		},
		{
			name: "only user-level installed passes without warning",
			routes: []antigravityHookRoute{
				absentRoute(antigravityRouteWorkspaceLabel),
				healthyRoute(antigravityRouteUserLabel),
				absentRoute(antigravityRoutePluginLabel),
			},
			wantStatus:   doctorStatusPass,
			wantContains: []string{antigravityRouteUserLabel},
		},
		{
			name: "only CLI plugin installed passes without warning",
			routes: []antigravityHookRoute{
				absentRoute(antigravityRouteWorkspaceLabel),
				absentRoute(antigravityRouteUserLabel),
				healthyRoute(antigravityRoutePluginLabel),
			},
			wantStatus:   doctorStatusPass,
			wantContains: []string{antigravityRoutePluginLabel},
		},
		{
			name: "none installed warns with actionable install message",
			routes: []antigravityHookRoute{
				absentRoute(antigravityRouteWorkspaceLabel),
				absentRoute(antigravityRouteUserLabel),
				absentRoute(antigravityRoutePluginLabel),
			},
			wantStatus:   doctorStatusWarn,
			wantContains: []string{"no supported Antigravity hook route", "--global", "agy plugin install"},
		},
		{
			name: "present but unhealthy route warns and points at per-route checks",
			routes: []antigravityHookRoute{
				absentRoute(antigravityRouteWorkspaceLabel),
				absentRoute(antigravityRouteUserLabel),
				presentUnhealthyRoute(antigravityRoutePluginLabel),
			},
			wantStatus:   doctorStatusWarn,
			wantContains: []string{antigravityRoutePluginLabel, "per-route checks"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := antigravityHookRouteSummary(tt.routes)
			if check.Name != antigravityRouteSummaryCheck {
				t.Fatalf("Name = %q, want %q", check.Name, antigravityRouteSummaryCheck)
			}
			if check.Status != tt.wantStatus {
				t.Fatalf("Status = %q, want %q (message: %q)", check.Status, tt.wantStatus, check.Message)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(check.Message, want) {
					t.Fatalf("message missing %q: %q", want, check.Message)
				}
			}
		})
	}
}

// TestAntigravityHookRouteChecksEmitsPerRoutePlusSummary confirms the per-route
// checks are emitted in order, followed by the single aggregate summary.
func TestAntigravityHookRouteChecksEmitsPerRoutePlusSummary(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	routes := []antigravityHookRoute{
		{Label: antigravityRouteWorkspaceLabel, Check: doctorCheck{Name: antigravityRouteWorkspaceCheck, Status: doctorStatusSkip}},
		{Label: antigravityRouteUserLabel, Healthy: true, Present: true, Check: doctorCheck{Name: antigravityRouteUserCheck, Status: doctorStatusPass}},
		{Label: antigravityRoutePluginLabel, Check: doctorCheck{Name: "antigravity-cli-plugin", Status: doctorStatusSkip}},
	}
	checks := antigravityHookRouteChecks(routes)
	wantNames := []string{antigravityRouteWorkspaceCheck, antigravityRouteUserCheck, "antigravity-cli-plugin", antigravityRouteSummaryCheck}
	if len(checks) != len(wantNames) {
		t.Fatalf("got %d checks, want %d", len(checks), len(wantNames))
	}
	for i, want := range wantNames {
		if checks[i].Name != want {
			t.Fatalf("checks[%d].Name = %q, want %q", i, checks[i].Name, want)
		}
	}
	if checks[len(checks)-1].Status != doctorStatusPass {
		t.Fatalf("summary status = %q, want pass (a healthy route present)", checks[len(checks)-1].Status)
	}
}
