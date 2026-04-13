#!/usr/bin/env python3
"""Verify changelog coverage for released Traceary versions."""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
VERSION_FILE = REPO_ROOT / "VERSION"
EN_CHANGELOG = REPO_ROOT / "CHANGELOG.md"
JA_CHANGELOG = REPO_ROOT / "CHANGELOG.ja.md"
VERSION_RE = re.compile(r"^v\d+\.\d+\.\d+$")
CHANGELOG_HEADING_RE = re.compile(r"^## \[(v\d+\.\d+\.\d+)\] - ", re.MULTILINE)


def fail(message: str) -> None:
    print(f"error: {message}", file=sys.stderr)
    raise SystemExit(1)


def read_release_version() -> str:
    version = VERSION_FILE.read_text(encoding="utf-8").strip()
    tag = f"v{version}"
    if not VERSION_RE.fullmatch(tag):
        fail(f"VERSION must contain a semantic version like 0.5.0, got {version!r}")
    return tag


def read_changelog_versions(path: Path) -> list[str]:
    text = path.read_text(encoding="utf-8")
    versions = CHANGELOG_HEADING_RE.findall(text)
    if not versions:
        fail(f"{path.name} does not contain any release headings")

    duplicates = sorted({version for version in versions if versions.count(version) > 1})
    if duplicates:
        fail(f"{path.name} contains duplicate release headings: {', '.join(duplicates)}")

    return versions


def git_release_tags() -> list[str]:
    result = subprocess.run(
        ["git", "tag", "--list", "v*", "--sort=version:refname"],
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
        check=True,
    )
    return [tag for tag in result.stdout.splitlines() if VERSION_RE.fullmatch(tag)]


def version_key(tag: str) -> tuple[int, int, int]:
    normalized = tag.removeprefix("v")
    major, minor, patch = normalized.split(".")
    return int(major), int(minor), int(patch)


def main() -> int:
    current_release = read_release_version()
    en_versions = read_changelog_versions(EN_CHANGELOG)
    ja_versions = read_changelog_versions(JA_CHANGELOG)

    if en_versions != ja_versions:
        missing_in_ja = [version for version in en_versions if version not in ja_versions]
        missing_in_en = [version for version in ja_versions if version not in en_versions]
        problems: list[str] = []
        if missing_in_ja:
            problems.append(f"missing in {JA_CHANGELOG.name}: {', '.join(missing_in_ja)}")
        if missing_in_en:
            problems.append(f"missing in {EN_CHANGELOG.name}: {', '.join(missing_in_en)}")
        if not problems:
            problems.append("release heading order differs between CHANGELOG.md and CHANGELOG.ja.md")
        fail("; ".join(problems))

    if current_release not in en_versions:
        fail(f"{EN_CHANGELOG.name} is missing the current VERSION entry {current_release}")
    if current_release not in ja_versions:
        fail(f"{JA_CHANGELOG.name} is missing the current VERSION entry {current_release}")

    tags = git_release_tags()
    if not tags:
        print(
            "warning: no semantic release tags were found locally; verified only VERSION and bilingual changelog parity",
            file=sys.stderr,
        )
        print("changelog release coverage check passed")
        return 0

    current_release_key = version_key(current_release)
    relevant_tags = [tag for tag in tags if version_key(tag) <= current_release_key]

    missing_from_changelog = [tag for tag in relevant_tags if tag not in en_versions]
    if missing_from_changelog:
        fail(
            "missing changelog coverage for released tags: "
            + ", ".join(missing_from_changelog)
            + " (up to the current VERSION; run in a full clone or update CHANGELOG.md / CHANGELOG.ja.md)"
        )

    print("changelog release coverage check passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
