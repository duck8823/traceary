package cli

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

// WithRuntimeClient injects only named non-starting local management calls.
func WithRuntimeClient(client app.Client) RootCLIOption {
	return func(c *RootCLI) { c.runtimeClient = client }
}

// WithRuntimeOperators injects explicit private metadata creation/open boundaries.
func WithRuntimeOperators(factory app.OperatorFactory) RootCLIOption {
	return func(c *RootCLI) { c.runtimeOperators = factory }
}

// WithNativeSessionMetadata injects the optional metadata-only native hook sink.
func WithNativeSessionMetadata(sink app.NativeSessionMetadataSink) RootCLIOption {
	return func(c *RootCLI) { c.nativeSessionMetadata = sink }
}
func writeRuntimeJSON(cmd *cobra.Command, value any) error {
	if err := json.NewEncoder(cmd.OutOrStdout()).Encode(value); err != nil {
		return xerrors.Errorf("write runtime metadata: %w", err)
	}
	return nil
}
func (c *RootCLI) operator(ctx context.Context, root string, create bool) (app.OperatorServices, error) {
	if c.runtimeOperators == nil {
		return app.OperatorServices{}, app.ErrRuntimeUnsupported
	}
	if create {
		services, err := c.runtimeOperators.Create(ctx, root)
		if err != nil {
			return app.OperatorServices{}, xerrors.Errorf("create operator services: %w", err)
		}
		return services, nil
	}
	services, err := c.runtimeOperators.Open(ctx, root)
	if err != nil {
		return app.OperatorServices{}, xerrors.Errorf("open operator services: %w", err)
	}
	return services, nil
}
func requireRuntimeFlags(cmd *cobra.Command, names ...string) {
	for _, name := range names {
		if err := cmd.MarkFlagRequired(name); err != nil {
			cmd.PreRunE = func(*cobra.Command, []string) error { return app.ErrRuntimeProtocol }
		}
	}
}
func (c *RootCLI) newRuntimeCommand() *cobra.Command {
	var root string
	group := &cobra.Command{Use: "runtime", Short: "Manage explicit local runtime targets and owned launch receipts"}
	group.AddCommand(c.newNativeSessionMarkerCommand(), c.newAntigravityStatuslineCommand())
	group.PersistentFlags().StringVar(&root, "runtime-dir", "", "Absolute private runtime directory (same root as daemon serve)")
	// Operator commands run with a bounded context and never initialize the core DB.
	operator := func(cmd *cobra.Command, create bool) (app.OperatorServices, context.Context, context.CancelFunc, error) {
		ctx, cancel := context.WithTimeout(cmd.Context(), 8*time.Second)
		path, err := resolveRuntimeDir(root)
		if err != nil {
			return app.OperatorServices{}, ctx, cancel, err
		}
		services, err := c.operator(ctx, path, create)
		return services, ctx, cancel, err
	}
	project := &cobra.Command{Use: "project", Short: "Register canonical Git projects"}
	var projectPath string
	registerProject := &cobra.Command{Use: "register", Short: "Register a canonical project and print JSON metadata", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		services, ctx, cancel, err := operator(cmd, true)
		defer cancel()
		if err != nil {
			return xerrors.Errorf("%s: %w", cmd.CommandPath(), err)
		}
		p, err := services.Registry.RegisterProject(ctx, projectPath)
		if err != nil {
			return xerrors.Errorf("%s: %w", cmd.CommandPath(), err)
		}
		return writeRuntimeJSON(cmd, projectView(p))
	}}
	registerProject.Flags().StringVar(&projectPath, "path", "", "Existing Git project path")
	requireRuntimeFlags(registerProject, "path")
	project.AddCommand(registerProject)
	worktree := &cobra.Command{Use: "worktree", Short: "Register immutable existing worktree/store bindings"}
	var projectID, worktreePath, storePath string
	registerWorktree := &cobra.Command{Use: "register", Short: "Bind an existing worktree and core-store identity; no DB creation", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		services, ctx, cancel, err := operator(cmd, true)
		defer cancel()
		if err != nil {
			return xerrors.Errorf("%s: %w", cmd.CommandPath(), err)
		}
		w, err := services.Registry.RegisterWorktree(ctx, domain.ProjectID(projectID), worktreePath, storePath)
		if err != nil {
			return xerrors.Errorf("%s: %w", cmd.CommandPath(), err)
		}
		return writeRuntimeJSON(cmd, worktreeView(w))
	}}
	registerWorktree.Flags().StringVar(&projectID, "project", "", "Registered project ID")
	registerWorktree.Flags().StringVar(&worktreePath, "path", "", "Existing Git worktree path")
	registerWorktree.Flags().StringVar(&storePath, "db-path", "", "Existing store path (identity only; never opened or migrated)")
	requireRuntimeFlags(registerWorktree, "project", "path", "db-path")
	worktree.AddCommand(registerWorktree)
	registry := &cobra.Command{Use: "registry", Short: "Read private registry metadata"}
	registry.AddCommand(&cobra.Command{Use: "list", Short: "Print registered project/worktree JSON without creating a root", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		services, ctx, cancel, err := operator(cmd, false)
		defer cancel()
		if err != nil {
			return xerrors.Errorf("%s: %w", cmd.CommandPath(), err)
		}
		state, err := services.Registry.List(ctx)
		if err != nil {
			return xerrors.Errorf("%s: %w", cmd.CommandPath(), err)
		}
		projects := make([]runtimeProjectView, 0, len(state.Projects))
		for _, p := range state.Projects {
			projects = append(projects, projectView(p))
		}
		worktrees := make([]runtimeWorktreeView, 0, len(state.Worktrees))
		for _, w := range state.Worktrees {
			worktrees = append(worktrees, worktreeView(w))
		}
		return writeRuntimeJSON(cmd, struct {
			Revision  uint64                `json:"revision"`
			Projects  []runtimeProjectView  `json:"projects"`
			Worktrees []runtimeWorktreeView `json:"worktrees"`
		}{state.Revision, projects, worktrees})
	}})
	profile := &cobra.Command{Use: "profile", Short: "Explicitly register fixed host executable/version policy"}
	var profileID, host, executable, version string
	registerProfile := &cobra.Command{Use: "register", Short: "Inspect a real executable with --version and save immutable policy", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		services, ctx, cancel, err := operator(cmd, true)
		defer cancel()
		if err != nil {
			return xerrors.Errorf("%s: %w", cmd.CommandPath(), err)
		}
		p, err := services.Profiles.Register(ctx, profileID, host, executable, version)
		if err != nil {
			return xerrors.Errorf("%s: %w", cmd.CommandPath(), err)
		}
		return writeRuntimeJSON(cmd, profileView(p))
	}}
	registerProfile.Flags().StringVar(&profileID, "id", "", "New immutable operator profile ID")
	registerProfile.Flags().StringVar(&host, "host", "", "Host: codex, claude, antigravity or grok")
	registerProfile.Flags().StringVar(&executable, "executable", "", "Explicit real executable path (no wrapper or shell command)")
	registerProfile.Flags().StringVar(&version, "expected-version", "", "Exact non-sensitive --version output (never credentials)")
	requireRuntimeFlags(registerProfile, "id", "host", "executable", "expected-version")
	profile.AddCommand(registerProfile)
	profile.AddCommand(&cobra.Command{Use: "list", Short: "Print profile metadata without executing hosts or creating a root", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		services, ctx, cancel, err := operator(cmd, false)
		defer cancel()
		if err != nil {
			return xerrors.Errorf("%s: %w", cmd.CommandPath(), err)
		}
		profiles, err := services.Profiles.List(ctx)
		if err != nil {
			return xerrors.Errorf("%s: %w", cmd.CommandPath(), err)
		}
		views := make([]runtimeProfileView, 0, len(profiles))
		for _, p := range profiles {
			views = append(views, profileView(p))
		}
		return writeRuntimeJSON(cmd, views)
	}})
	var epoch, selectedWorktree, selectedProfile, requestID string
	launch := &cobra.Command{Use: "launch", Short: "Explicitly launch once using registered IDs; no executable/argv/env overrides", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if c.runtimeClient == nil {
			return app.ErrRuntimeUnsupported
		}
		path, err := resolveRuntimeDir(root)
		if err != nil {
			return xerrors.Errorf("%s: %w", cmd.CommandPath(), err)
		}
		request := app.LaunchRequest{Epoch: epoch, Worktree: domain.WorktreeID(selectedWorktree), Profile: domain.ProfileID(selectedProfile), RequestID: requestID}
		run, err := c.runtimeClient.Launch(cmd.Context(), path, request)
		if err != nil {
			// Echo only explicit public request identity so transport uncertainty is not
			// mistaken for no launch or silently replaced by another idempotency key.
			outcome := "rejected"
			if errors.Is(err, app.ErrLaunchUnknown) {
				outcome = "unknown"
			}
			if writeErr := writeRuntimeJSON(cmd, struct {
				RequestID string `json:"request_id"`
				Epoch     string `json:"epoch"`
				Outcome   string `json:"outcome"`
			}{requestID, epoch, outcome}); writeErr != nil {
				return writeErr
			}
			return xerrors.Errorf("explicit runtime launch: %w", err)
		}
		return writeRuntimeJSON(cmd, struct {
			RequestID string         `json:"request_id"`
			Run       runtimeRunView `json:"run"`
		}{requestID, runView(run)})
	}}
	launch.Flags().StringVar(&epoch, "epoch", "", "Current daemon epoch from daemon status --json")
	launch.Flags().StringVar(&selectedWorktree, "worktree", "", "Registered immutable worktree ID")
	launch.Flags().StringVar(&selectedProfile, "profile", "", "Registered immutable profile ID")
	launch.Flags().StringVar(&requestID, "request-id", "", "Explicit unique request ID; reuse only to inspect the same receipt")
	requireRuntimeFlags(launch, "epoch", "worktree", "profile", "request-id")
	var listEpoch, cursor string
	var revision uint64
	list := &cobra.Command{Use: "list", Short: "Print one bounded epoch/revision-bound managed-run JSON page", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if c.runtimeClient == nil {
			return app.ErrRuntimeUnsupported
		}
		path, err := resolveRuntimeDir(root)
		if err != nil {
			return xerrors.Errorf("%s: %w", cmd.CommandPath(), err)
		}
		page, err := c.runtimeClient.ListRuns(cmd.Context(), path, app.RunPageRequest{Epoch: listEpoch, Cursor: cursor, Revision: revision})
		if err != nil {
			return xerrors.Errorf("%s: %w", cmd.CommandPath(), err)
		}
		runs := make([]runtimeRunView, 0, len(page.Runs))
		for _, r := range page.Runs {
			runs = append(runs, runView(r))
		}
		return writeRuntimeJSON(cmd, struct {
			Epoch    string           `json:"epoch"`
			Revision uint64           `json:"revision"`
			Runs     []runtimeRunView `json:"runs"`
			Next     string           `json:"next,omitempty"`
		}{page.Epoch, page.Revision, runs, page.Next})
	}}
	list.Flags().StringVar(&listEpoch, "epoch", "", "Current daemon epoch")
	list.Flags().StringVar(&cursor, "cursor", "", "Previous page next cursor")
	list.Flags().Uint64Var(&revision, "revision", 0, "Previous page revision (required with cursor)")
	requireRuntimeFlags(list, "epoch")
	group.AddCommand(project, worktree, registry, profile, launch, list, c.newRuntimeResumeCommand(&root), c.newRuntimeAttachCommand(&root), c.newRuntimeHistoryCommand(&root), c.newRuntimeMetadataCommand(&root), c.newRuntimeObserveCommand(&root))
	for _, kind := range []string{"input", "resize", "stop", "receipt"} {
		group.AddCommand(c.newRuntimeControlCommand(&root, kind))
	}
	return group
}

type runtimeProjectView struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Common string `json:"common_dir"`
}

func projectView(p domain.Project) runtimeProjectView {
	return runtimeProjectView{string(p.ID()), p.Root(), p.CommonDir()}
}

type runtimeWorktreeView struct {
	ID      string `json:"id"`
	Project string `json:"project_id"`
	Path    string `json:"path"`
	Common  string `json:"common_dir"`
	Store   string `json:"store"`
}

func worktreeView(w domain.Worktree) runtimeWorktreeView {
	return runtimeWorktreeView{string(w.ID()), string(w.Project()), w.Path(), w.CommonDir(), w.Store()}
}

type runtimeProfileView struct {
	ID         string `json:"id"`
	Host       string `json:"host"`
	Executable string `json:"executable"`
	Version    string `json:"version"`
}

func profileView(p domain.HostProfile) runtimeProfileView {
	s := p.Snapshot()
	return runtimeProfileView{s.ID, s.Host, s.Executable, s.Version}
}

type runtimeRunView struct {
	Run          string          `json:"run"`
	Generation   string          `json:"generation"`
	Epoch        string          `json:"epoch"`
	Worktree     string          `json:"worktree"`
	Profile      string          `json:"profile"`
	ProcessState domain.RunState `json:"process_state"`
	PID          int             `json:"diagnostic_pid"`
	ExitCode     int             `json:"exit_code"`
	Uncertain    bool            `json:"publication_uncertain"`
}

func runView(r domain.RunSnapshot) runtimeRunView {
	return runtimeRunView{r.Identity.Run, r.Identity.Generation, r.Identity.Epoch, string(r.Target.Worktree().ID()), string(r.Profile.ID()), r.State, r.PID, r.ExitCode, r.PublicationUncertain}
}
