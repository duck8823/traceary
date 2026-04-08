#!/usr/bin/env python3
from __future__ import annotations

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
HOOK_SOURCES = [
    ROOT / 'scripts' / 'hooks' / 'common.sh',
    ROOT / 'scripts' / 'hooks' / 'traceary-session.sh',
    ROOT / 'scripts' / 'hooks' / 'traceary-audit.sh',
]
HOOK_PACKAGES = [
    ROOT / 'integrations' / 'claude-plugin' / 'scripts',
    ROOT / 'plugins' / 'traceary' / 'scripts',
    ROOT / 'integrations' / 'gemini-extension' / 'scripts',
]


def fail(message: str) -> None:
    print(f'error: {message}', file=sys.stderr)
    raise SystemExit(1)


def read_json(path: Path) -> dict:
    try:
        return json.loads(path.read_text(encoding='utf-8'))
    except FileNotFoundError as exc:
        fail(f'missing file: {path.relative_to(ROOT)}')
    except json.JSONDecodeError as exc:
        fail(f'invalid json in {path.relative_to(ROOT)}: {exc}')


def require(condition: bool, message: str) -> None:
    if not condition:
        fail(message)


def check_hooks_are_copied() -> None:
    for source in HOOK_SOURCES:
        source_text = source.read_text(encoding='utf-8')
        for package_dir in HOOK_PACKAGES:
            target = package_dir / source.name
            require(target.exists(), f'missing packaged hook script: {target.relative_to(ROOT)}')
            require(
                target.read_text(encoding='utf-8') == source_text,
                f'packaged hook script drifted from canonical source: {target.relative_to(ROOT)}',
            )


def check_claude() -> None:
    marketplace = read_json(ROOT / '.claude-plugin' / 'marketplace.json')
    require(marketplace['name'] == 'traceary-plugins', 'unexpected Claude marketplace name')
    require(len(marketplace.get('plugins', [])) == 1, 'Claude marketplace must expose exactly one plugin')
    plugin_entry = marketplace['plugins'][0]
    require(plugin_entry['name'] == 'traceary', 'unexpected Claude plugin name')
    require(plugin_entry['source'] == './integrations/claude-plugin', 'unexpected Claude plugin source path')

    plugin_manifest = read_json(ROOT / 'integrations' / 'claude-plugin' / '.claude-plugin' / 'plugin.json')
    require(plugin_manifest['version'] == '0.1.12', 'Claude plugin version must track v0.1.12')

    mcp = read_json(ROOT / 'integrations' / 'claude-plugin' / '.mcp.json')
    require(mcp['traceary']['command'] == 'traceary', 'Claude MCP must call traceary')
    require(mcp['traceary']['args'] == ['mcp-server'], 'Claude MCP args must be traceary mcp-server')

    hooks = read_json(ROOT / 'integrations' / 'claude-plugin' / 'hooks' / 'hooks.json')
    require('SessionStart' in hooks['hooks'], 'Claude hooks must include SessionStart')
    require('SessionEnd' in hooks['hooks'], 'Claude hooks must include SessionEnd')
    require('PostToolUse' in hooks['hooks'], 'Claude hooks must include PostToolUse')
    require((ROOT / 'integrations' / 'claude-plugin' / 'skills' / 'traceary-help' / 'SKILL.md').exists(), 'missing Claude traceary-help skill')
    require((ROOT / 'integrations' / 'claude-plugin' / 'skills' / 'traceary-session-history' / 'SKILL.md').exists(), 'missing Claude traceary-session-history skill')


def check_codex() -> None:
    marketplace = read_json(ROOT / '.agents' / 'plugins' / 'marketplace.json')
    require(marketplace['name'] == 'traceary-marketplace', 'unexpected Codex marketplace name')
    require(len(marketplace.get('plugins', [])) == 1, 'Codex marketplace must expose exactly one plugin')
    entry = marketplace['plugins'][0]
    require(entry['name'] == 'traceary', 'unexpected Codex plugin name')
    require(entry['source']['path'] == './plugins/traceary', 'unexpected Codex plugin source path')

    plugin_manifest = read_json(ROOT / 'plugins' / 'traceary' / '.codex-plugin' / 'plugin.json')
    require(plugin_manifest['version'] == '0.1.12', 'Codex plugin version must track v0.1.12')

    mcp = read_json(ROOT / 'plugins' / 'traceary' / '.mcp.json')
    traceary = mcp['mcpServers']['traceary']
    require(traceary['command'] == 'traceary', 'Codex MCP must call traceary')
    require(traceary['args'] == ['mcp-server'], 'Codex MCP args must be traceary mcp-server')

    hooks = read_json(ROOT / 'plugins' / 'traceary' / 'hooks.json')
    require('SessionStart' in hooks['hooks'], 'Codex hooks must include SessionStart')
    require('Stop' in hooks['hooks'], 'Codex hooks must include Stop')
    require('PostToolUse' in hooks['hooks'], 'Codex hooks must include PostToolUse')
    require((ROOT / 'plugins' / 'traceary' / 'commands' / 'help.md').exists(), 'missing Codex help command')
    require((ROOT / 'plugins' / 'traceary' / 'commands' / 'doctor.md').exists(), 'missing Codex doctor command')
    require((ROOT / 'plugins' / 'traceary' / 'skills' / 'traceary-session-history' / 'SKILL.md').exists(), 'missing Codex traceary-session-history skill')


def check_gemini() -> None:
    manifest = read_json(ROOT / 'integrations' / 'gemini-extension' / 'gemini-extension.json')
    require(manifest['name'] == 'traceary', 'unexpected Gemini extension name')
    require(manifest['version'] == '0.1.12', 'Gemini extension version must track v0.1.12')
    traceary = manifest['mcpServers']['traceary']
    require(traceary['command'] == 'traceary', 'Gemini MCP must call traceary')
    require(traceary['args'] == ['mcp-server'], 'Gemini MCP args must be traceary mcp-server')
    require(manifest['contextFileName'] == 'GEMINI.md', 'Gemini extension must expose GEMINI.md as context file')

    hooks = read_json(ROOT / 'integrations' / 'gemini-extension' / 'hooks' / 'hooks.json')
    require('SessionStart' in hooks['hooks'], 'Gemini hooks must include SessionStart')
    require('SessionEnd' in hooks['hooks'], 'Gemini hooks must include SessionEnd')
    require('AfterTool' in hooks['hooks'], 'Gemini hooks must include AfterTool')
    require((ROOT / 'integrations' / 'gemini-extension' / 'commands' / 'traceary-help.toml').exists(), 'missing Gemini help command')
    require((ROOT / 'integrations' / 'gemini-extension' / 'commands' / 'traceary-doctor.toml').exists(), 'missing Gemini doctor command')
    require((ROOT / 'integrations' / 'gemini-extension' / 'skills' / 'traceary-session-history' / 'SKILL.md').exists(), 'missing Gemini traceary-session-history skill')
    require((ROOT / 'integrations' / 'gemini-extension' / 'GEMINI.md').exists(), 'missing Gemini context file')


def check_docs() -> None:
    expected_pairs = [
        ROOT / 'docs' / 'integrations' / 'README.md',
        ROOT / 'docs' / 'integrations' / 'claude-plugin.md',
        ROOT / 'docs' / 'integrations' / 'codex-plugin.md',
        ROOT / 'docs' / 'integrations' / 'gemini-extension.md',
    ]
    for english in expected_pairs:
        japanese = english.with_suffix('.ja.md')
        require(japanese.exists(), f'missing Japanese docs pair for {english.relative_to(ROOT)}')


def main() -> None:
    check_hooks_are_copied()
    check_claude()
    check_codex()
    check_gemini()
    check_docs()
    print('ok: integration manifests and packaged assets are consistent')


if __name__ == '__main__':
    main()
