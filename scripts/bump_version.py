#!/usr/bin/env python3
"""Bump the version string in VERSION and all plugin/extension manifests."""
from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

VERSION_FILE = ROOT / 'VERSION'
MANIFESTS = [
    ROOT / 'integrations' / 'claude-plugin' / '.claude-plugin' / 'plugin.json',
    ROOT / 'integrations' / 'gemini-extension' / 'gemini-extension.json',
    ROOT / 'plugins' / 'traceary' / '.codex-plugin' / 'plugin.json',
]

LANDING_INDEX = ROOT / 'docs' / 'landing' / 'index.html'
LANDING_COMPONENTS = ROOT / 'docs' / 'landing' / 'components.jsx'


def validate_version(version: str) -> str:
    if not re.fullmatch(r'\d+\.\d+\.\d+', version):
        print(f'error: version must be in X.Y.Z format, got: {version}', file=sys.stderr)
        raise SystemExit(1)
    return version


def bump(version: str) -> None:
    VERSION_FILE.write_text(version + '\n', encoding='utf-8')
    print(f'  VERSION -> {version}')

    for manifest_path in MANIFESTS:
        content = manifest_path.read_text(encoding='utf-8')
        updated = re.sub(
            r'"version"\s*:\s*"[^"]*"',
            f'"version": "{version}"',
            content,
            count=1,
        )
        if updated == content:
            print(f'  warning: no version field found in {manifest_path.relative_to(ROOT)}', file=sys.stderr)
        manifest_path.write_text(updated, encoding='utf-8')
        print(f'  {manifest_path.relative_to(ROOT)} -> {version}')

    bump_landing(version)


def bump_landing(version: str) -> None:
    major_minor = '.'.join(version.split('.')[:2])

    index_content = LANDING_INDEX.read_text(encoding='utf-8')
    updated_index = re.sub(
        r'(<span class="hero-eyebrow"><span class="dot"></span>)v\d+\.\d+(\b)',
        rf'\g<1>v{major_minor}\g<2>',
        index_content,
        count=1,
    )
    if updated_index == index_content:
        print(
            f'  warning: hero eyebrow version marker not found in {LANDING_INDEX.relative_to(ROOT)}',
            file=sys.stderr,
        )
    LANDING_INDEX.write_text(updated_index, encoding='utf-8')
    print(f'  {LANDING_INDEX.relative_to(ROOT)} -> v{major_minor}')

    components_content = LANDING_COMPONENTS.read_text(encoding='utf-8')
    updated_components = re.sub(r'traceary--\d+\.\d+\.\d+', f'traceary--{version}', components_content)
    updated_components = re.sub(
        r'(/Cellar/traceary/)\d+\.\d+\.\d+', rf'\g<1>{version}', updated_components,
    )
    if updated_components == components_content:
        print(
            f'  warning: version markers not found in {LANDING_COMPONENTS.relative_to(ROOT)}',
            file=sys.stderr,
        )
    LANDING_COMPONENTS.write_text(updated_components, encoding='utf-8')
    print(f'  {LANDING_COMPONENTS.relative_to(ROOT)} -> {version}')


def main() -> None:
    parser = argparse.ArgumentParser(description='Bump version across all manifests')
    parser.add_argument('--version', required=True, help='Target version (X.Y.Z)')
    args = parser.parse_args()

    version = validate_version(args.version)
    print(f'Bumping version to {version}:')
    bump(version)
    print(
        'Done. Run `python3 scripts/verify_release_manifests.py`, '
        '`go run ./cmd/repo-tooling integrations verify`, and '
        '`python3 scripts/verify_landing.py` to verify.'
    )


if __name__ == '__main__':
    main()
