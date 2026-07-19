package filesystem

import (
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// KimiHooksHandler is the client-specific boundary for Kimi Code hooks.
// Native event mapping and installation remain disabled until capture wiring
// is implemented by the dependent runtime issue.
type KimiHooksHandler struct{}

// NewKimiHooksHandler constructs the Kimi hook boundary.
func NewKimiHooksHandler() *KimiHooksHandler { return &KimiHooksHandler{} }

// Name returns the canonical client identifier.
func (h *KimiHooksHandler) Name() string { return "kimi" }

// Build returns an empty, deterministic hook document. This deliberately
// reaches a registered Kimi boundary without claiming that capture is wired;
// the actual capture wiring lands in the dependent runtime issue.
func (h *KimiHooksHandler) Build(_ string) model.Hooks {
	return model.HooksOf([]string{}, map[string][]model.HookEntry{})
}

// DefaultInstallPath fails closed until native runtime support is
// implemented; the Traceary Kimi plugin will be the distribution path.
func (h *KimiHooksHandler) DefaultInstallPath(_ string) (string, error) {
	return "", kimiInstallUnavailableError()
}

func (h *KimiHooksHandler) validateInstall() error {
	return kimiInstallUnavailableError()
}

func kimiInstallUnavailableError() error {
	return xerrors.Errorf("kimi hook installation is not available until native runtime support is implemented; the Traceary Kimi plugin (kimi.plugin.json) will be the distribution path")
}
