package application

// HookScriptsInstaller materializes the bundled Traceary hook scripts on
// disk and resolves the directory that contains them.
type HookScriptsInstaller interface {
	// Ensure installs the bundled scripts to disk and returns the resolved
	// scripts directory.
	Ensure() (string, error)

	// ResolveDir returns the scripts directory without installing.
	ResolveDir() (string, error)
}
