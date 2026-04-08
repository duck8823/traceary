#!/usr/bin/env python3
"""Verify English/Japanese documentation pairs."""

from __future__ import annotations

import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
TOP_LEVEL_DOCS = {
    Path("README.md"),
    Path("CHANGELOG.md"),
}
TOP_LEVEL_DOCS_JA = {Path("README.ja.md"), Path("CHANGELOG.ja.md")}
TOP_LINES_TO_CHECK = 8


def ja_variant(path: Path) -> Path:
    return path.with_name(f"{path.stem}.ja{path.suffix}")


def en_variant(path: Path) -> Path:
    if not path.name.endswith(".ja.md"):
        raise ValueError(f"not a Japanese markdown path: {path}")
    return path.with_name(path.name.removesuffix(".ja.md") + ".md")


def is_in_scope(path: Path) -> bool:
    if path in TOP_LEVEL_DOCS or path in TOP_LEVEL_DOCS_JA:
        return True

    if path.parts and path.parts[0] == "docs":
        return path.suffix == ".md"

    return False


def collect_markdown_files() -> tuple[set[Path], set[Path]]:
    english: set[Path] = set(TOP_LEVEL_DOCS)
    japanese: set[Path] = set(TOP_LEVEL_DOCS_JA)

    for abs_path in (REPO_ROOT / "docs").rglob("*.md"):
        rel_path = abs_path.relative_to(REPO_ROOT)
        if rel_path.name.endswith(".ja.md"):
            japanese.add(rel_path)
            continue

        english.add(rel_path)

    english = {path for path in english if is_in_scope(path)}
    japanese = {path for path in japanese if is_in_scope(path)}

    return english, japanese


def first_lines(path: Path) -> list[str]:
    return (REPO_ROOT / path).read_text(encoding="utf-8").splitlines()[:TOP_LINES_TO_CHECK]


def verify_language_switch(path: Path, expected_label: str, expected_targets: list[str]) -> list[str]:
    content = "\n".join(first_lines(path))
    errors: list[str] = []
    if expected_label not in content:
        errors.append(f"{path}: missing language switch label {expected_label!r} near the top")
    if not any(target in content for target in expected_targets):
        joined_targets = ", ".join(repr(target) for target in expected_targets)
        errors.append(f"{path}: missing language switch target near the top; expected one of {joined_targets}")
    return errors


def expected_targets_for_pair(path: Path) -> list[str]:
    if path.name.endswith(".ja.md"):
        pair = en_variant(path)
    else:
        pair = ja_variant(path)

    return [f"({pair.name})", f"(./{pair.name})"]


def main() -> int:
    english, japanese = collect_markdown_files()
    errors: list[str] = []

    for path in sorted(english):
        abs_path = REPO_ROOT / path
        if not abs_path.exists():
            errors.append(f"{path}: English document is missing")
            continue

        ja_path = ja_variant(path)
        if ja_path not in japanese or not (REPO_ROOT / ja_path).exists():
            errors.append(f"{path}: missing Japanese pair {ja_path}")
            continue

        errors.extend(verify_language_switch(path, "[日本語]", expected_targets_for_pair(path)))

    for path in sorted(japanese):
        abs_path = REPO_ROOT / path
        if not abs_path.exists():
            errors.append(f"{path}: Japanese document is missing")
            continue

        en_path = en_variant(path)
        if en_path not in english or not (REPO_ROOT / en_path).exists():
            errors.append(f"{path}: missing English pair {en_path}")
            continue

        errors.extend(verify_language_switch(path, "[English]", expected_targets_for_pair(path)))

    if errors:
        print("documentation i18n check failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1

    print("documentation i18n check passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
