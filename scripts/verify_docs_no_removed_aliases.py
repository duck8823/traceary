#!/usr/bin/env python3
"""Verify documentation does not recommend removed command aliases.

The aliases listed below cover the v0.14.0 top-level removals (see #918) and
the v0.15.0 removals of the hidden memory flat verbs plus
`integration codex uninstall` (see #956 and #957). Tagged release notes, the
CLI stability and deprecation policy, and the memory command surface plan must
keep mentioning them — that is where the migration table lives. Any other
Markdown file under docs/ or at the repository root is expected to use the
canonical replacement command instead.

The check also flags the v0.15-removed flat memory aliases (`memory remember`,
`memory accept`, `memory hygiene scan`, ...) **including bare forms without the
`traceary` prefix**, because operator-facing CLI docs frequently quote exact
remediation snippets like ``memory activate --apply``. Documentation should
recommend the canonical `memory store ...` / `memory inbox ...` /
`memory admin ...` namespace forms instead. Migration tables, CHANGELOG history,
and the deprecation policy are file-level allow-listed because they need to
quote the legacy names by design. Migration table rows that live inside
otherwise non-allow-listed files (for example, the
``Hidden deprecated aliases (v0.14)`` table in ``docs/cli/README.md``) are
recognised by a narrow row-level pattern: a Markdown table row whose body
also references a canonical ``memory inbox|store|admin`` form. Multi-word
aliases use exact command stems (``memory hygiene scan``,
``memory graph add``, ``memory import codex``, ...) so conceptual phrases
like ``memory graph edge`` or family-name mentions of ``memory inbox`` /
``memory hygiene`` do not trip the check. The host-native activation ADR
is *not* file-level allow-listed; only the precise `DO NOT EDIT`
marker-contract lines it quotes from `application/usecase/import_stub_block.go`
and `application/usecase/memory_export.go` are allowed (see
`ALLOWED_LINE_HINTS`).

Run this script as part of CI (and locally before opening a PR) so a stale
example like `traceary handoff --workspace ...` cannot slip back into a guide
without an explicit decision.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent

# AI agent instruction files — not user-facing documentation. Excluded to
# match the scope of scripts/verify_docs_i18n.py.
SCOPE_EXCLUDE = {"CLAUDE.md", "AGENTS.md", "GEMINI.md"}

# Removed top-level aliases (v0.14.0). Each pattern matches the literal
# command path so the canonical `traceary store init` / `traceary session
# handoff` / etc. forms do not match.
V014_REMOVED_ALIAS_PATTERNS: tuple[tuple[str, str], ...] = (
    (r"\btraceary init\b", "traceary store init"),
    (r"\btraceary backup\b", "traceary store backup ..."),
    (r"\btraceary gc\b", "traceary store gc"),
    (r"\btraceary handoff\b", "traceary session handoff"),
    (r"\btraceary compact-summary\b", "traceary session handoff --compact-only"),
)

# Removed Codex integration aliases. `integration codex install` was already
# retired in v0.14.0, and v0.15.0 removes the paired cleanup-only `uninstall`
# surface (#957). Guard both names so docs cannot recommend half of the
# retired Codex management surface.
V015_CODEX_ALIAS_PATTERNS: tuple[tuple[str, str], ...] = (
    (
        r"\btraceary integration codex install\b",
        "Codex official /plugins flow",
    ),
    (
        r"\btraceary integration codex uninstall\b",
        "Codex official /plugins flow plus docs/integrations/codex-plugin.md manual cleanup steps",
    ),
)

# Flat memory aliases removed in v0.15.0. Each pattern matches the *bare* form
# so it catches snippets like ``memory activate --apply`` quoted by operator
# docs in addition to ``traceary memory activate``. The bare-form regex matches
# the prefixed form too because of the word boundary (``\bmemory remember``
# inside ``traceary memory remember`` is still a match), so a single pattern set
# covers both shapes. Multi-word aliases pin the exact stem (``memory hygiene
# scan``, ``memory graph add``, ``memory import codex``, ...) to avoid false
# positives on conceptual phrases like ``memory graph edge``. Parent namespace
# aliases such as ``memory import`` / ``memory hygiene`` / ``memory graph`` are
# only matched when they look like a complete command token (end, punctuation,
# a closing backtick, or flags) so prose like "memory graph edge" is allowed.
# ``memory inbox`` does not appear because it is a canonical v0.14 form, not a
# deprecated alias.
V015_MEMORY_ALIAS_PATTERNS: tuple[tuple[str, str], ...] = (
    (r"\bmemory remember\b", "memory store remember"),
    (r"\bmemory propose\b", "memory store propose"),
    (r"\bmemory distill\b", "memory store distill"),
    (r"\bmemory accept\b", "memory inbox accept"),
    (r"\bmemory reject\b", "memory inbox reject"),
    (r"\bmemory extract\b", "memory admin extract"),
    (r"\bmemory supersede\b", "memory admin supersede"),
    (r"\bmemory expire\b", "memory admin expire"),
    (r"\bmemory set-validity\b", "memory admin set-validity"),
    (r"\bmemory import(?=\s*(?:`|$|[.,;:)\]]|--))", "memory admin import"),
    (r"\bmemory import codex\b", "memory admin import codex"),
    (r"\bmemory import instructions\b", "memory admin import instructions"),
    (r"\bmemory export\b", "memory admin export"),
    (r"\bmemory activate\b", "memory admin activate"),
    (r"\bmemory hygiene(?=\s*(?:`|$|[.,;:)\]]|--))", "memory admin hygiene"),
    (r"\bmemory hygiene scan\b", "memory admin hygiene scan"),
    (r"\bmemory hygiene apply\b", "memory admin hygiene apply"),
    (r"\bmemory graph(?=\s*(?:`|$|[.,;:)\]]|--))", "memory admin graph"),
    (r"\bmemory graph add\b", "memory admin graph add"),
    (r"\bmemory graph list\b", "memory admin graph list"),
)

ALL_PATTERNS: tuple[tuple[str, str], ...] = (
    V014_REMOVED_ALIAS_PATTERNS
    + V015_CODEX_ALIAS_PATTERNS
    + V015_MEMORY_ALIAS_PATTERNS
)

# Files that are allowed to mention the removed aliases. CHANGELOG entries
# describe history, the CLI stability / deprecation policy ships the
# migration table, and the v0.14 memory command surface plan documents the
# old → new mapping.
ALLOWED_FILES: frozenset[str] = frozenset(
    {
        "CHANGELOG.md",
        "CHANGELOG.ja.md",
        "docs/cli-stability.md",
        "docs/cli-stability.ja.md",
        "docs/operations/memory-command-surface.md",
        "docs/operations/memory-command-surface.ja.md",
    }
)

# Lines that frame the legacy alias name inside an explicit migration
# context, or are literal Traceary-managed marker text quoted from the
# on-disk file format. Such lines are allowed everywhere because they are
# precisely the kind of guidance the removed-alias policy expects, or they
# are byte-for-byte contract text owned by
# `application/usecase/import_stub_block.go` and
# `application/usecase/memory_export.go`. The marker-contract hints are
# deliberately narrow: they match the literal `DO NOT EDIT: this import is
# managed by Traceary` and `DO NOT EDIT: this block is managed by Traceary`
# warnings rendered into managed regions, not arbitrary prose that happens
# to mention `traceary memory activate`.
ALLOWED_LINE_HINTS: tuple[str, ...] = (
    "removed in v0.14",
    "removed in v0.15",
    "retired in v0.15",
    "retired, hidden",
    "v0.14.0 で削除",
    "v0.15.0 で削除",
    "v0.15.0 で廃止",
    "v0.14 以前の `traceary integration codex install`",
    "廃止・非表示",
    "hidden deprecated alias",
    "hidden な deprecated alias",
    "DO NOT EDIT: this import is managed by Traceary",
    "DO NOT EDIT: this block is managed by Traceary",
)

# Markdown migration-table row pattern. A row is allowed when it is a
# table row (starts with `|`) whose body also references a canonical
# v0.14 ``memory inbox``, ``memory store``, or ``memory admin`` form.
# This covers the old-alias → canonical-replacement rows in the v0.14
# memory deprecation table without allow-listing every line in
# ``docs/cli/README.md``.
MIGRATION_ROW_RE = re.compile(r"^\s*\|.*\bmemory (inbox|store|admin)\b")
REMOVAL_CONTEXT_RE = re.compile(
    r"v0\.1[45](?:\.0)?",
    re.IGNORECASE,
)
REMOVAL_WORD_RE = re.compile(
    r"removed|retired|deprecated|scheduled for removal|hidden stubs?|削除|廃止",
    re.IGNORECASE,
)


def is_in_scope(path: Path) -> bool:
    """Match the same scope as scripts/verify_docs_i18n.py."""
    if path.parts and path.parts[0] == "docs":
        return path.suffix == ".md"
    if path.name in SCOPE_EXCLUDE:
        return False
    return path.parent == Path(".") and path.suffix == ".md"


def collect_markdown_files() -> list[Path]:
    files: set[Path] = set()
    for abs_path in REPO_ROOT.glob("*.md"):
        rel_path = abs_path.relative_to(REPO_ROOT)
        if is_in_scope(rel_path):
            files.add(rel_path)
    for abs_path in (REPO_ROOT / "docs").rglob("*.md"):
        rel_path = abs_path.relative_to(REPO_ROOT)
        if is_in_scope(rel_path):
            files.add(rel_path)
    return sorted(files)


def is_allowed_line(line: str) -> bool:
    if any(hint in line for hint in ALLOWED_LINE_HINTS):
        return True
    if REMOVAL_CONTEXT_RE.search(line) and REMOVAL_WORD_RE.search(line):
        return True
    return MIGRATION_ROW_RE.search(line) is not None


def scan_file(path: Path) -> list[str]:
    rel_str = str(path).replace("\\", "/")
    if rel_str in ALLOWED_FILES:
        return []
    text = (REPO_ROOT / path).read_text(encoding="utf-8")
    findings: list[str] = []
    for line_index, line in enumerate(text.splitlines(), start=1):
        if is_allowed_line(line):
            continue
        for pattern, replacement in ALL_PATTERNS:
            if re.search(pattern, line):
                findings.append(
                    f"{rel_str}:{line_index}: removed alias matching {pattern!r}; "
                    f"use `{replacement}` instead. Line: {line.strip()}"
                )
    return findings


def main() -> int:
    findings: list[str] = []
    for path in collect_markdown_files():
        findings.extend(scan_file(path))
    if findings:
        print("removed-alias documentation check failed:", file=sys.stderr)
        for finding in findings:
            print(f"- {finding}", file=sys.stderr)
        return 1
    print("removed-alias documentation check passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
