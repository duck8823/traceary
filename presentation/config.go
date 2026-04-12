package presentation

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// ConfigLoadStatus describes the result of attempting to load the optional
// operator config file.
type ConfigLoadStatus string

const (
	// ConfigLoadStatusLoaded indicates that config.json was read successfully.
	ConfigLoadStatusLoaded ConfigLoadStatus = "loaded"
	// ConfigLoadStatusMissing indicates that config.json is not present.
	ConfigLoadStatusMissing ConfigLoadStatus = "missing"
	// ConfigLoadStatusInvalid indicates that config.json exists but is invalid.
	ConfigLoadStatusInvalid ConfigLoadStatus = "invalid"
	// ConfigLoadStatusUnreadable indicates that config.json exists but could not be read.
	ConfigLoadStatusUnreadable ConfigLoadStatus = "unreadable"
	// ConfigLoadStatusHomeDirFailure indicates that the config path could not be resolved.
	ConfigLoadStatusHomeDirFailure ConfigLoadStatus = "home_dir_failure"
)

// ConfigInspection describes the outcome of reading the optional config file
// for diagnostic reporting (e.g. by the doctor command).
type ConfigInspection struct {
	Path                string
	Status              ConfigLoadStatus
	Err                 error
	ExtraRedactPatterns []string
}

// configFile mirrors the on-disk JSON layout. It is intentionally unexported
// because callers receive the loaded values directly.
type configFile struct {
	Redact redactSection `json:"redact"`
}

type redactSection struct {
	ExtraPatterns []string `json:"extra_patterns"`
}

// InspectConfig reads the Traceary config from ~/.config/traceary/config.json
// without emitting operator-facing log output.
func InspectConfig() ConfigInspection {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ConfigInspection{
			Status: ConfigLoadStatusHomeDirFailure,
			Err:    err,
		}
	}

	configPath := filepath.Join(homeDir, ".config", "traceary", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ConfigInspection{
				Path:   configPath,
				Status: ConfigLoadStatusMissing,
			}
		}
		return ConfigInspection{
			Path:   configPath,
			Status: ConfigLoadStatusUnreadable,
			Err:    err,
		}
	}

	var file configFile
	if err := json.Unmarshal(data, &file); err != nil {
		return ConfigInspection{
			Path:   configPath,
			Status: ConfigLoadStatusInvalid,
			Err:    err,
		}
	}

	return ConfigInspection{
		Path:                configPath,
		Status:              ConfigLoadStatusLoaded,
		ExtraRedactPatterns: file.Redact.ExtraPatterns,
	}
}

func (i ConfigInspection) warningMessage() string {
	switch i.Status {
	case ConfigLoadStatusInvalid:
		return fmt.Sprintf(
			"Traceary config is invalid; extra audit redaction patterns are disabled until the file is fixed: %s",
			i.Path,
		)
	case ConfigLoadStatusUnreadable:
		return fmt.Sprintf(
			"Traceary config could not be read; extra audit redaction patterns are disabled until the file is readable: %s",
			i.Path,
		)
	case ConfigLoadStatusHomeDirFailure:
		return "Traceary config path could not be resolved; extra audit redaction patterns are disabled"
	default:
		return ""
	}
}

// LoadExtraRedactPatterns reads the optional Traceary config and returns the
// extra redaction patterns. When the config cannot be used the returned slice
// is nil and a warning is logged via slog.
func LoadExtraRedactPatterns() []string {
	inspection := InspectConfig()
	if warningMessage := inspection.warningMessage(); warningMessage != "" {
		slog.Warn(warningMessage, "error", inspection.Err)
	}

	return inspection.ExtraRedactPatterns
}
