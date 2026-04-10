package presentation

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// Config holds user configuration loaded from config.json.
type Config struct {
	Redact RedactConfig `json:"redact"`
}

// RedactConfig holds redaction-related configuration.
type RedactConfig struct {
	ExtraPatterns []string `json:"extra_patterns"`
}

// ConfigLoadStatus describes the result of attempting to load the optional
// operator config file.
type ConfigLoadStatus string

const (
	// ConfigLoadStatusLoaded indicates that config.json was read successfully.
	ConfigLoadStatusLoaded         ConfigLoadStatus = "loaded"
	// ConfigLoadStatusMissing indicates that config.json is not present.
	ConfigLoadStatusMissing        ConfigLoadStatus = "missing"
	// ConfigLoadStatusInvalid indicates that config.json exists but is invalid.
	ConfigLoadStatusInvalid        ConfigLoadStatus = "invalid"
	// ConfigLoadStatusUnreadable indicates that config.json exists but could not be read.
	ConfigLoadStatusUnreadable     ConfigLoadStatus = "unreadable"
	// ConfigLoadStatusHomeDirFailure indicates that the config path could not be resolved.
	ConfigLoadStatusHomeDirFailure ConfigLoadStatus = "home_dir_failure"
)

// ConfigLoadResult describes the outcome of reading the optional config file.
type ConfigLoadResult struct {
	Config Config
	Path   string
	Status ConfigLoadStatus
	Err    error
}

// InspectConfig reads the Traceary config from ~/.config/traceary/config.json
// without emitting operator-facing log output.
func InspectConfig() ConfigLoadResult {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ConfigLoadResult{
			Status: ConfigLoadStatusHomeDirFailure,
			Err:    err,
		}
	}

	configPath := filepath.Join(homeDir, ".config", "traceary", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ConfigLoadResult{
				Path:   configPath,
				Status: ConfigLoadStatusMissing,
			}
		}
		return ConfigLoadResult{
			Path:   configPath,
			Status: ConfigLoadStatusUnreadable,
			Err:    err,
		}
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return ConfigLoadResult{
			Path:   configPath,
			Status: ConfigLoadStatusInvalid,
			Err:    err,
		}
	}

	return ConfigLoadResult{
		Config: config,
		Path:   configPath,
		Status: ConfigLoadStatusLoaded,
	}
}

func (r ConfigLoadResult) warningMessage() string {
	switch r.Status {
	case ConfigLoadStatusInvalid:
		return fmt.Sprintf(
			"Traceary config is invalid; extra audit redaction patterns are disabled until the file is fixed: %s",
			r.Path,
		)
	case ConfigLoadStatusUnreadable:
		return fmt.Sprintf(
			"Traceary config could not be read; extra audit redaction patterns are disabled until the file is readable: %s",
			r.Path,
		)
	case ConfigLoadStatusHomeDirFailure:
		return "Traceary config path could not be resolved; extra audit redaction patterns are disabled"
	default:
		return ""
	}
}

// LoadConfig reads the Traceary config from ~/.config/traceary/config.json.
// Returns a zero-value Config when the optional config cannot be used.
func LoadConfig() Config {
	result := InspectConfig()
	if warningMessage := result.warningMessage(); warningMessage != "" {
		slog.Warn(warningMessage, "error", result.Err)
	}

	return result.Config
}
