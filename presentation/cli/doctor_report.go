package cli

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/xerrors"
)

func writeDoctorReport(output io.Writer, report *doctorReport, asJSON bool, warningsOK bool) error {
	if report == nil {
		return xerrors.New(Localize("doctor report must not be nil", "doctor report は nil にできません"))
	}
	finalizeDoctorReport(report, warningsOK)

	if asJSON {
		return writeJSON(output, report)
	}

	if _, err := fmt.Fprintln(output, "TRACEARY DOCTOR"); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print doctor header", "doctor ヘッダーの出力に失敗しました"), err)
	}
	if report.DBPath != "" {
		if _, err := fmt.Fprintf(output, "DB_PATH: %s\n", report.DBPath); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print DB path", "DB パスの出力に失敗しました"), err)
		}
	}
	if _, err := fmt.Fprintf(output, "SUMMARY: pass=%d warn=%d fail=%d exit_code=%d\n", report.Summary.Pass, report.Summary.Warn, report.Summary.Fail, report.ExitCode); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print doctor summary", "doctor サマリーの出力に失敗しました"), err)
	}
	if len(report.Fixes) > 0 {
		if _, err := fmt.Fprintln(output, "\nFixes"); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print doctor fixes", "doctor 修復結果の出力に失敗しました"), err)
		}
		for _, fix := range report.Fixes {
			line := fmt.Sprintf("- %s: %s (before=%s after=%s)", fix.Name, fix.Action, fix.Before, fix.After)
			if fix.Error != "" {
				line += ": " + fix.Error
			}
			if _, err := fmt.Fprintln(output, line); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print doctor fix", "doctor 修復結果の出力に失敗しました"), err)
			}
		}
	}

	for _, section := range report.Sections {
		if len(section.Checks) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(output, "\n%s\n", section.Name); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print doctor section", "doctor セクションの出力に失敗しました"), err)
		}
		for _, check := range section.Checks {
			if _, err := fmt.Fprintf(output, "[%s] %s: %s\n", check.Severity, check.Name, check.Message); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print doctor check", "doctor チェックの出力に失敗しました"), err)
			}
			if check.Hint != "" {
				if _, err := fmt.Fprintf(output, "  hint: %s\n", check.Hint); err != nil {
					return xerrors.Errorf("%s: %w", Localize("failed to print doctor check hint", "doctor チェックヒントの出力に失敗しました"), err)
				}
			}
		}
	}

	return nil
}

func finalizeDoctorReport(report *doctorReport, warningsOK bool) {
	if report == nil {
		return
	}
	for i := range report.Checks {
		applyDoctorSeverity(&report.Checks[i])
		report.Checks[i].Section = doctorSectionNameForCheck(report.Checks[i].Name)
	}
	report.Summary = doctorSummary{}
	for _, check := range report.Checks {
		switch check.Severity {
		case doctorSeverityFail:
			report.Summary.Fail++
		case doctorSeverityWarn:
			report.Summary.Warn++
		default:
			report.Summary.Pass++
		}
	}
	switch {
	case report.Summary.Fail > 0:
		report.ExitCode = 1
	case report.Summary.Warn > 0:
		if warningsOK {
			report.ExitCode = 0
		} else {
			report.ExitCode = 2
		}
	default:
		report.ExitCode = 0
	}
	report.Sections = buildDoctorSections(report.Checks)
}

func applyDoctorSeverity(check *doctorCheck) {
	if check == nil {
		return
	}
	if check.Status == "" {
		check.Status = doctorStatusPass
	}
	switch check.Status {
	case doctorStatusFail:
		check.Severity = doctorSeverityFail
	case doctorStatusWarn:
		check.Severity = doctorSeverityWarn
	default:
		check.Severity = doctorSeverityPass
	}
}

func buildDoctorSections(checks []doctorCheck) []doctorSection {
	sections := []doctorSection{
		{Name: "Environment", Checks: []doctorCheck{}},
		{Name: "Database", Checks: []doctorCheck{}},
		{Name: "Plugins", Checks: []doctorCheck{}},
		{Name: "MCP", Checks: []doctorCheck{}},
		{Name: "Hooks", Checks: []doctorCheck{}},
	}
	index := map[string]int{}
	for i, section := range sections {
		index[section.Name] = i
	}
	for _, check := range checks {
		sectionName := doctorSectionNameForCheck(check.Name)
		sections[index[sectionName]].Checks = append(sections[index[sectionName]].Checks, check)
	}
	return sections
}

func doctorSectionNameForCheck(name string) string {
	switch {
	case name == "db-path" || name == "db-write" || name == "stale-active-sessions" || name == "audit-reliability" || name == "content-event-reliability":
		return "Database"
	case name == "config" || name == "project-dir" || name == "version" || name == "path":
		return "Environment"
	case strings.Contains(name, "plugin"):
		return "Plugins"
	case strings.HasSuffix(name, "-host-capabilities") || strings.HasSuffix(name, "-mcp") || strings.HasPrefix(name, "mcp"):
		return "MCP"
	default:
		return "Hooks"
	}
}
