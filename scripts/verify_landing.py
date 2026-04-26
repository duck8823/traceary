#!/usr/bin/env python3
"""Verify the docs/landing/ assets stay in sync with VERSION.

The release-prep flow rewrites version markers via scripts/bump_version.py;
this script is the CI-side guard that fails the build when the landing page
drifts from VERSION.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
VERSION_FILE = ROOT / 'VERSION'
LANDING_INDEX = ROOT / 'docs' / 'landing' / 'index.html'
LANDING_COMPONENTS = ROOT / 'docs' / 'landing' / 'components.jsx'


def fail(message: str) -> None:
    print(f'error: {message}', file=sys.stderr)
    raise SystemExit(1)


def rel(path: Path) -> str:
    return str(path.relative_to(ROOT))


def main() -> None:
    if not VERSION_FILE.exists():
        fail(f'missing {rel(VERSION_FILE)}')
    version = VERSION_FILE.read_text(encoding='utf-8').strip()
    if not re.fullmatch(r'\d+\.\d+\.\d+', version):
        fail(f'{rel(VERSION_FILE)} is not X.Y.Z: {version!r}')
    major_minor = '.'.join(version.split('.')[:2])

    if not LANDING_INDEX.exists():
        fail(f'missing {rel(LANDING_INDEX)}')
    index_text = LANDING_INDEX.read_text(encoding='utf-8')
    eyebrow_pattern = (
        r'<span class="hero-eyebrow"><span class="dot"></span>v(\d+\.\d+)\b'
    )
    match = re.search(eyebrow_pattern, index_text)
    if match is None:
        fail(f'{rel(LANDING_INDEX)}: hero eyebrow version marker not found')
    if match.group(1) != major_minor:
        fail(
            f'{rel(LANDING_INDEX)}: hero eyebrow says v{match.group(1)} '
            f'but VERSION is {version} (expected v{major_minor})'
        )

    if not LANDING_COMPONENTS.exists():
        fail(f'missing {rel(LANDING_COMPONENTS)}')
    components_text = LANDING_COMPONENTS.read_text(encoding='utf-8')

    bottle_pattern = r'traceary--(\d+\.\d+\.\d+)'
    bottle_versions = set(re.findall(bottle_pattern, components_text))
    if not bottle_versions:
        fail(f'{rel(LANDING_COMPONENTS)}: no `traceary--X.Y.Z` bottle markers found')
    drift = bottle_versions - {version}
    if drift:
        fail(
            f'{rel(LANDING_COMPONENTS)}: bottle versions {sorted(drift)} '
            f'do not match VERSION {version}'
        )

    cellar_pattern = r'/Cellar/traceary/(\d+\.\d+\.\d+)'
    cellar_versions = set(re.findall(cellar_pattern, components_text))
    if not cellar_versions:
        fail(f'{rel(LANDING_COMPONENTS)}: no `/Cellar/traceary/X.Y.Z` markers found')
    cellar_drift = cellar_versions - {version}
    if cellar_drift:
        fail(
            f'{rel(LANDING_COMPONENTS)}: Cellar versions {sorted(cellar_drift)} '
            f'do not match VERSION {version}'
        )

    print(f'OK: docs/landing/ in sync with VERSION {version}')


if __name__ == '__main__':
    main()
