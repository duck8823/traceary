package cli

import (
	"os"
	"strings"
)

const doctorStoreIndependentLabel = "store-independent"

func doctorDBPathWasExplicit(flagPath string) bool {
	return strings.TrimSpace(flagPath) != "" || strings.TrimSpace(os.Getenv(dbPathEnvKey)) != ""
}

func doctorCheckIsStoreIndependent(name string) bool {
	switch strings.TrimSpace(name) {
	case "path", "config", "version", "project-dir",
		"hook-spool", "hook-state-residue", "hook-memory-extract", "hook-grok-transcript",
		"claude-hook-cancellations", "claude-plugin-cache":
		return true
	}
	if strings.HasSuffix(name, "-plugin") || strings.HasSuffix(name, "-plugin-version") || strings.HasSuffix(name, "-inspect") {
		return true
	}
	return strings.Contains(name, "plugin-cache")
}

func isTracearyStoreAddressedCommand(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) < 2 || fields[0] != "traceary" {
		return false
	}
	switch fields[1] {
	case "doctor", "store", "memory":
		return true
	default:
		return false
	}
}

func withDoctorInspectedDBPath(command, dbPath string) string {
	command = strings.TrimSpace(command)
	if dbPath == "" || command == "" {
		return command
	}
	if strings.Contains(command, "--db-path") {
		return command
	}
	if !isTracearyStoreAddressedCommand(command) {
		return command
	}
	return command + " --db-path " + shellQuote(dbPath)
}

func rewriteBacktickedStoreCommands(text, dbPath string) string {
	if dbPath == "" || !strings.Contains(text, "`") {
		return text
	}
	parts := strings.Split(text, "`")
	for i := 1; i < len(parts); i += 2 {
		parts[i] = withDoctorInspectedDBPath(parts[i], dbPath)
	}
	return strings.Join(parts, "`")
}

func annotateDoctorScopeAndDBPathHints(report *doctorReport) {
	if report == nil {
		return
	}
	dbPath := strings.TrimSpace(report.hintDBPath)
	for i := range report.Checks {
		check := &report.Checks[i]
		if doctorCheckIsStoreIndependent(check.Name) && check.Message != "" && !strings.Contains(check.Message, doctorStoreIndependentLabel) {
			check.Message = strings.TrimRight(check.Message, ".") + " (" + doctorStoreIndependentLabel + ")"
		}
		if dbPath == "" {
			continue
		}
		check.FixCommand = withDoctorInspectedDBPath(check.FixCommand, dbPath)
		check.Hint = rewriteBacktickedStoreCommands(check.Hint, dbPath)
		check.Message = rewriteBacktickedStoreCommands(check.Message, dbPath)
	}
}
