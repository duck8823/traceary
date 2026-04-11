package cli

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"
)

var detectRepoContextFunc = detectRepoContext

func resolveWorkspaceValue(ctx context.Context, flagValue string) string {
	if repo := resolveExplicitWorkspaceValue(flagValue); repo != "" {
		return repo
	}

	repo, err := detectRepoContextFunc(ctx)
	if err != nil {
		slog.Debug("repo context detection failed, using empty value", "error", err)
		return ""
	}

	return repo
}

func resolveExplicitWorkspaceValue(flagValue string) string {
	return resolveOptionalValue(flagValue, "TRACEARY_WORKSPACE", "")
}

func detectRepoContext(ctx context.Context) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to get current working directory", "現在の working directory の取得に失敗しました"), err)
	}

	return detectRepoContextFromDir(ctx, cwd)
}

func detectRepoContextFromDir(ctx context.Context, cwd string) (string, error) {
	trimmedCWD := strings.TrimSpace(cwd)
	if trimmedCWD == "" {
		return "", xerrors.Errorf(Localize("working directory must not be empty", "working directory は空にできません"))
	}

	remote, remoteErr := gitOutput(ctx, trimmedCWD, "config", "--get", "remote.origin.url")
	if remoteErr == nil {
		normalized := normalizeGitRemoteURL(remote)
		if normalized != "" {
			return normalized, nil
		}
	}

	repoRoot, repoRootErr := gitOutput(ctx, trimmedCWD, "rev-parse", "--show-toplevel")
	if repoRootErr == nil {
		if remoteErr != nil {
			slog.Debug("git remote not available, using local worktree root", "error", remoteErr, "worktree", strings.TrimSpace(repoRoot))
		}
		normalized := normalizeLocalWorkContextPath(repoRoot)
		if normalized != "" {
			return normalized, nil
		}
	}

	if remoteErr != nil && repoRootErr != nil {
		return "", xerrors.Errorf(
			"%s: %w",
			Localize("failed to detect git work context", "git work context の検出に失敗しました"),
			repoRootErr,
		)
	}

	return "", xerrors.Errorf(Localize("git work context could not be resolved", "git work context を解決できませんでした"))
}

func gitOutput(ctx context.Context, cwd string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", cwd}, args...)
	output, err := exec.CommandContext(ctx, "git", commandArgs...).Output()
	if err != nil {
		return "", xerrors.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}

	return string(output), nil
}

func normalizeGitRemoteURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimSuffix(trimmed, ".git")
	if trimmed == "" {
		return ""
	}

	if strings.HasPrefix(trimmed, "git@") {
		hostAndPath := strings.TrimPrefix(trimmed, "git@")
		parts := strings.SplitN(hostAndPath, ":", 2)
		if len(parts) == 2 {
			return strings.ToLower(parts[0]) + "/" + strings.Trim(parts[1], "/")
		}
	}

	parsedURL, err := url.Parse(trimmed)
	if err == nil && parsedURL.Host != "" {
		return strings.ToLower(parsedURL.Host) + "/" + strings.Trim(parsedURL.Path, "/")
	}

	return trimmed
}

func normalizeLocalWorkContextPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	resolvedPath, err := filepath.Abs(trimmed)
	if err != nil {
		resolvedPath = trimmed
	}

	return filepath.ToSlash(filepath.Clean(resolvedPath))
}
