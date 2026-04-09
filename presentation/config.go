package presentation

import (
	"encoding/json"
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

// LoadConfig reads the Traceary config from ~/.config/traceary/config.json.
// Returns a zero-value Config if the file does not exist or cannot be parsed.
func LoadConfig() Config {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		slog.Debug("failed to get home directory for config", "error", err)
		return Config{}
	}

	configPath := filepath.Join(homeDir, ".config", "traceary", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Debug("failed to read config file", "path", configPath, "error", err)
		}
		return Config{}
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		slog.Debug("failed to parse config file", "path", configPath, "error", err)
		return Config{}
	}

	return config
}
