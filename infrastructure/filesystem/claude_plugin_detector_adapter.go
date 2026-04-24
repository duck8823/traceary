package filesystem

import "github.com/duck8823/traceary/application"

// ClaudePluginDetectorAdapter implements application.ClaudePluginDetector
// by delegating to the existing DetectClaudeTracearyPluginIn function.
type ClaudePluginDetectorAdapter struct{}

// NewClaudePluginDetectorAdapter constructs a detector adapter.
func NewClaudePluginDetectorAdapter() *ClaudePluginDetectorAdapter {
	return &ClaudePluginDetectorAdapter{}
}

// DetectClaudeTracearyPluginIn adapts the infrastructure value object
// to the application-level ClaudePluginDetection shape.
func (a *ClaudePluginDetectorAdapter) DetectClaudeTracearyPluginIn(home string) application.ClaudePluginDetection {
	detection := DetectClaudeTracearyPluginIn(home)
	return application.ClaudePluginDetection{
		Active:       detection.Active,
		SettingsPath: detection.SettingsPath,
		PluginKey:    detection.PluginKey,
	}
}
