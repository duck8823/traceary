package cli_test

import (
	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/infrastructure/filesystem"
	"github.com/duck8823/traceary/presentation/cli"
)

// newTestHooksOptions returns the hook-related options used by the majority
// of CLI tests. They wire filesystem-backed orchestrator and inspector
// implementations while honoring the test's SetUserHomeDirFunc override
// (the orchestrator calls the function at the moment Install is invoked, so
// overrides installed after option construction still apply).
func newTestHooksOptions() []cli.RootCLIOption {
	// Use a closure that always delegates to the package-level
	// CallUserHomeDirFunc so test overrides installed after this helper is
	// invoked (via cli.SetUserHomeDirFunc) still apply at call time.
	homeDirFunc := func() (string, error) {
		return cli.CallUserHomeDirFunc()
	}

	return []cli.RootCLIOption{
		cli.WithHooksOrchestrator(filesystem.NewHooksOrchestrator(map[string]application.HooksClientHandler{
			"claude": filesystem.NewClaudeHooksHandler(),
			"codex":  filesystem.NewCodexHooksHandlerWithHomeDirFunc(homeDirFunc),
			"gemini": filesystem.NewGeminiHooksHandler(),
		})),
		cli.WithHooksInspector(filesystem.NewHooksInspector()),
		cli.WithPluginCacheInspector(filesystem.NewPluginCacheInspector()),
		cli.WithClaudePluginDetector(filesystem.NewClaudePluginDetectorAdapter()),
	}
}

// newTestRootCLI constructs a RootCLI with the given options plus the
// default hook-related options used by CLI tests.
func newTestRootCLI(opts ...cli.RootCLIOption) *cli.RootCLI {
	combined := make([]cli.RootCLIOption, 0, len(opts)+3)
	combined = append(combined, newTestHooksOptions()...)
	combined = append(combined, opts...)
	return cli.NewRootCLI(combined...)
}
