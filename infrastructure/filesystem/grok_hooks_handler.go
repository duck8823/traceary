package filesystem

import (
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

// GrokHooksHandler is the client-specific boundary for Grok Build hooks.
// Native event mapping and installation remain disabled until the Grok host
// contract is implemented by the dependent runtime issue.
type GrokHooksHandler struct{}

// NewGrokHooksHandler constructs the Grok hook boundary.
func NewGrokHooksHandler() *GrokHooksHandler { return &GrokHooksHandler{} }

// Name returns the canonical client identifier.
func (h *GrokHooksHandler) Name() string { return "grok" }

// Build returns an empty, deterministic hook document. This deliberately
// reaches a registered Grok boundary without claiming that capture is wired.
func (h *GrokHooksHandler) Build(_ string) model.Hooks {
	return model.HooksOf([]string{}, map[string][]model.HookEntry{})
}

// DefaultInstallPath fails closed until the Grok hook configuration contract
// and runtime event mapping are implemented.
func (h *GrokHooksHandler) DefaultInstallPath(_ string) (string, error) {
	return "", xerrors.Errorf("grok hook installation is not available until native runtime support is implemented")
}
