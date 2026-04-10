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


def main() -> None:
    parser = argparse.ArgumentParser(description='Bump version across all manifests')
    parser.add_argument('--version', required=True, help='Target version (X.Y.Z)')
    args = parser.parse_args()

    version = validate_version(args.version)
    print(f'Bumping version to {version}:')
    bump(version)
    print('Done. Run `python3 scripts/verify_integrations.py` to verify.')


if __name__ == '__main__':
    main()
