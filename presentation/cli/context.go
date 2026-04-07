package cli

import (
	"context"
	"net/url"
	"os/exec"
	"strings"

	"golang.org/x/xerrors"
)

var detectRepoContextFunc = detectRepoContext

func resolveRepoValue(ctx context.Context, flagValue string) string {
	if repo := resolveOptionalValue(flagValue, "TRACEARY_REPO", ""); repo != "" {
		return repo
	}

	repo, err := detectRepoContextFunc(ctx)
	if err != nil {
		return ""
	}

	return repo
}

func detectRepoContext(ctx context.Context) (string, error) {
	output, err := exec.CommandContext(ctx, "git", "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return "", xerrors.Errorf("git remote origin の取得に失敗しました: %w", err)
	}

	normalized := normalizeGitRemoteURL(string(output))
	if normalized == "" {
		return "", xerrors.Errorf("git remote origin が空です")
	}

	return normalized, nil
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
