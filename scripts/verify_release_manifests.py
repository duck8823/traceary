#!/usr/bin/env python3
"""Verify marketplace and plugin release manifests track VERSION."""
from __future__ import annotations

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
VERSION_FILE = ROOT / 'VERSION'

MARKETPLACE_MANIFESTS = [
    ROOT / '.claude-plugin' / 'marketplace.json',
    ROOT / '.agents' / 'plugins' / 'marketplace.json',
]

PLUGIN_MANIFESTS = [
    ROOT / 'integrations' / 'claude-plugin' / '.claude-plugin' / 'plugin.json',
    ROOT / 'integrations' / 'antigravity-plugin' / 'plugin.json',
    ROOT / 'integrations' / 'gemini-extension' / 'gemini-extension.json',
    ROOT / 'plugins' / 'traceary' / '.codex-plugin' / 'plugin.json',
]


def rel(path: Path) -> str:
    return str(path.relative_to(ROOT))


def fail(message: str) -> None:
    print(f'error: {message}', file=sys.stderr)
    raise SystemExit(1)


def read_json(path: Path) -> dict:
    if not path.exists():
        fail(f'missing manifest: {rel(path)}')
    try:
        return json.loads(path.read_text(encoding='utf-8'))
    except json.JSONDecodeError as exc:
        fail(f'invalid json in {rel(path)}: {exc}')


def read_version() -> str:
    if not VERSION_FILE.exists():
        fail('missing VERSION')
    version = VERSION_FILE.read_text(encoding='utf-8').strip()
    if not version:
        fail('VERSION is empty')
    return version


def require(condition: bool, message: str) -> None:
    if not condition:
        fail(message)


def check_marketplace_manifests() -> None:
    claude_marketplace = read_json(ROOT / '.claude-plugin' / 'marketplace.json')
    require(claude_marketplace.get('name') == 'traceary-plugins', 'unexpected Claude marketplace name')
    claude_plugins = claude_marketplace.get('plugins', [])
    require(len(claude_plugins) == 1, 'Claude marketplace must expose exactly one plugin')
    require(claude_plugins[0].get('name') == 'traceary', 'unexpected Claude marketplace plugin name')
    require(
        claude_plugins[0].get('source') == './integrations/claude-plugin',
        'Claude marketplace source must point at ./integrations/claude-plugin',
    )

    codex_marketplace = read_json(ROOT / '.agents' / 'plugins' / 'marketplace.json')
    require(codex_marketplace.get('name') == 'traceary-marketplace', 'unexpected Codex marketplace name')
    codex_plugins = codex_marketplace.get('plugins', [])
    require(len(codex_plugins) == 1, 'Codex marketplace must expose exactly one plugin')
    require(codex_plugins[0].get('name') == 'traceary', 'unexpected Codex marketplace plugin name')
    require(
        codex_plugins[0].get('source', {}).get('path') == './plugins/traceary',
        'Codex marketplace source path must point at ./plugins/traceary',
    )



def check_plugin_versions(expected_version: str) -> None:
    for path in PLUGIN_MANIFESTS:
        manifest = read_json(path)
        actual_version = manifest.get('version')
        require(
            actual_version == expected_version,
            f'{rel(path)} version {actual_version!r} must match VERSION {expected_version!r}; run `make release/bump VERSION={expected_version}` or update both files together',
        )


def main() -> None:
    version = read_version()
    # Make the existence contract explicit even when structural checks below change later.
    for path in MARKETPLACE_MANIFESTS + PLUGIN_MANIFESTS:
        read_json(path)
    check_marketplace_manifests()
    check_plugin_versions(version)
    print(f'ok: release manifests exist and plugin versions match VERSION {version}')


if __name__ == '__main__':
    main()
