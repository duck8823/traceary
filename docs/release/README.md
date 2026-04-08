# Release guide

[日本語](./README.ja.md)

Traceary supports two public installation paths.

## Install

### `go install`

Use `go install` when you want the latest tagged CLI in your Go bin directory.

```sh
go install github.com/duck8823/traceary@latest
```

If you prefer a specific release, pin the tag explicitly.

```sh
go install github.com/duck8823/traceary@v0.1.7
```

### Prebuilt binaries

Tagged releases publish compressed binaries for:

- macOS amd64 / arm64
- Linux amd64 / arm64

Download the archive that matches your platform from the GitHub Releases page and extract the `traceary` binary into a directory on `PATH`.

## Version metadata

Traceary exposes version metadata through `traceary --version`.

Two paths fill that metadata:

- `go install github.com/duck8823/traceary@<tag>` uses Go build info from the tagged module
- release binaries use GoReleaser `ldflags` from `.goreleaser.yml`

This keeps release builds from reporting the default `dev` version string.

## Release automation

`.github/workflows/release.yml` runs on `v*` tags and invokes the official GoReleaser GitHub Action.

That workflow:

1. checks out the full git history
2. sets up Go
3. runs GoReleaser in release mode for tag refs, or snapshot mode for manual branch runs
4. publishes GitHub release artifacts and checksums for tagged releases

`workflow_dispatch` is mainly for dry-running the pipeline on a branch. To publish an actual release, push a `v*` tag (or manually run the workflow against a tag ref).

## Local snapshot builds

Use the local snapshot target when you want to inspect release artifacts before tagging.

```sh
make release/snapshot
```

This runs `goreleaser release --snapshot --clean` and writes artifacts under `dist/`.

## References

- GitHub Releases: https://github.com/duck8823/traceary/releases
- GoReleaser GitHub Actions docs: https://goreleaser.com/customization/ci/actions/
- GoReleaser install docs: https://goreleaser.com/getting-started/install/
