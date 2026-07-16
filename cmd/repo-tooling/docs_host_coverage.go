package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/duck8823/traceary/application/hostcoverage"
	"golang.org/x/xerrors"
)

const (
	hostCoverageBeginMarker = "<!-- host-coverage-matrix:begin -->"
	hostCoverageEndMarker   = "<!-- host-coverage-matrix:end -->"
	hostCoverageGenComment  = "<!-- DO NOT EDIT: generated from application/hostcoverage/matrix.json via `go run ./cmd/repo-tooling docs generate-host-coverage`. -->"
)

var hostCoverageDocFiles = []struct {
	rel  string
	lang string
}{
	{rel: "docs/hooks/host-coverage.md", lang: "en"},
	{rel: "docs/hooks/host-coverage.ja.md", lang: "ja"},
}

func generateHostCoverageDocs(root string) error {
	matrix, err := hostcoverage.Load()
	if err != nil {
		return xerrors.Errorf("load host coverage matrix: %w", err)
	}
	for _, doc := range hostCoverageDocFiles {
		if err := rewriteHostCoverageDoc(filepath.Join(root, doc.rel), matrix.RenderMatrixTable(doc.lang)); err != nil {
			return err
		}
	}
	return nil
}

func verifyHostCoverageDocs(root string) error {
	matrix, err := hostcoverage.Load()
	if err != nil {
		return xerrors.Errorf("load host coverage matrix: %w", err)
	}
	for _, doc := range hostCoverageDocFiles {
		path := filepath.Join(root, doc.rel)
		data, err := os.ReadFile(path) // #nosec G304 -- fixed docs path under repo root
		if err != nil {
			return xerrors.Errorf("read %s: %w", doc.rel, err)
		}
		wantBody := hostCoverageGenComment + "\n" + strings.TrimSuffix(matrix.RenderMatrixTable(doc.lang), "\n")
		gotBody, err := extractHostCoverageSection(string(data))
		if err != nil {
			return xerrors.Errorf("%s: %w", doc.rel, err)
		}
		if strings.TrimSpace(gotBody) != strings.TrimSpace(wantBody) {
			return xerrors.Errorf("%s host-coverage matrix section drifted from application/hostcoverage/matrix.json; run `go run ./cmd/repo-tooling docs generate-host-coverage`", doc.rel)
		}
	}
	return nil
}

func rewriteHostCoverageDoc(path, table string) error {
	data, err := os.ReadFile(path) // #nosec G304 -- fixed docs path under repo root
	if err != nil {
		return xerrors.Errorf("read %s: %w", path, err)
	}
	updated, err := replaceHostCoverageSection(string(data), hostCoverageGenComment+"\n"+strings.TrimSuffix(table, "\n"))
	if err != nil {
		return xerrors.Errorf("%s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return xerrors.Errorf("write %s: %w", path, err)
	}
	return nil
}

func extractHostCoverageSection(content string) (string, error) {
	begin := strings.Index(content, hostCoverageBeginMarker)
	end := strings.Index(content, hostCoverageEndMarker)
	if begin < 0 || end < 0 || end <= begin {
		return "", xerrors.Errorf("missing %s / %s markers", hostCoverageBeginMarker, hostCoverageEndMarker)
	}
	start := begin + len(hostCoverageBeginMarker)
	return strings.TrimSpace(content[start:end]), nil
}

func replaceHostCoverageSection(content, body string) (string, error) {
	begin := strings.Index(content, hostCoverageBeginMarker)
	end := strings.Index(content, hostCoverageEndMarker)
	if begin < 0 || end < 0 || end <= begin {
		return "", xerrors.Errorf("missing %s / %s markers", hostCoverageBeginMarker, hostCoverageEndMarker)
	}
	var b strings.Builder
	b.WriteString(content[:begin])
	b.WriteString(hostCoverageBeginMarker)
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	b.WriteString(content[end:])
	return b.String(), nil
}
