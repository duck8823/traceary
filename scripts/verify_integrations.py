#!/usr/bin/env python3
from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
INTEGRATION_VERSION = (ROOT / 'VERSION').read_text(encoding='utf-8').strip()
# Pin the CLI to English so the removed-command smoke assertions are
# deterministic regardless of the operator's ui.language / OS locale
# (mirrors the presentation/cli TestMain locale pin and CI's English default).
ENGLISH_CLI_ENV = {**os.environ, 'TRACEARY_LANG': 'en'}
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
    require(plugin_manifest['version'] == INTEGRATION_VERSION, f'Claude plugin version must track v{INTEGRATION_VERSION}')

    mcp = read_json(ROOT / 'integrations' / 'claude-plugin' / '.mcp.json')
    require(mcp['traceary']['command'] == 'traceary', 'Claude MCP must call traceary')
    require(mcp['traceary']['args'] == ['mcp-server'], 'Claude MCP args must be traceary mcp-server')

    hooks = read_json(ROOT / 'integrations' / 'claude-plugin' / 'hooks' / 'hooks.json')
    require('SessionStart' in hooks['hooks'], 'Claude hooks must include SessionStart')
    require('SessionEnd' in hooks['hooks'], 'Claude hooks must include SessionEnd')
    require('PostToolUse' in hooks['hooks'], 'Claude hooks must include PostToolUse')
    require('PostCompact' in hooks['hooks'], 'Claude hooks must include PostCompact')
    require("'hook' 'session' 'claude'" in json.dumps(hooks['hooks']), 'Claude packaged hooks must invoke traceary hook session directly')
    require("'hook' 'audit' 'claude'" in json.dumps(hooks['hooks']), 'Claude packaged hooks must invoke traceary hook audit directly')

    # v0.8-6: both PostToolUse and PostToolUseFailure must register
    # three matchers (Bash / mcp__.* / built-in tool list) so Traceary
    # captures the real working surface, not just shell + MCP traffic.
    for event_name in ('PostToolUse', 'PostToolUseFailure'):
        entries = hooks['hooks'].get(event_name, [])
        matchers = [entry.get('matcher') for entry in entries]
        require(
            matchers[:2] == ['Bash', 'mcp__.*'],
            f'Claude {event_name} must register Bash and mcp__.* as the first two matchers, got {matchers!r}',
        )
        require(
            len(matchers) >= 3 and matchers[2] and 'Read' in matchers[2] and 'Edit' in matchers[2] and 'Write' in matchers[2],
            f'Claude {event_name} must include the built-in tool matcher (Read|Edit|Write|...), got {matchers!r}',
        )
    require((ROOT / 'integrations' / 'claude-plugin' / 'scripts' / 'traceary-compact.sh').exists(), 'missing Claude compact hook script')
    require((ROOT / 'integrations' / 'claude-plugin' / 'skills' / 'traceary-help' / 'SKILL.md').exists(), 'missing Claude traceary-help skill')
    require((ROOT / 'integrations' / 'claude-plugin' / 'skills' / 'traceary-session-history' / 'SKILL.md').exists(), 'missing Claude traceary-session-history skill')
    require((ROOT / 'integrations' / 'claude-plugin' / 'skills' / 'traceary-memory-review' / 'SKILL.md').exists(), 'missing Claude traceary-memory-review skill')
    require((ROOT / 'integrations' / 'claude-plugin' / 'skills' / 'traceary-memory-remember' / 'SKILL.md').exists(), 'missing Claude traceary-memory-remember skill')
    require(not (ROOT / 'integrations' / 'claude-plugin' / 'skills' / 'traceary-memory-capture').exists(), 'Claude traceary-memory-capture skill stub must be removed (replaced by traceary-memory-review and traceary-memory-remember)')


def check_codex() -> None:
    marketplace = read_json(ROOT / '.agents' / 'plugins' / 'marketplace.json')
    require(marketplace['name'] == 'traceary-marketplace', 'unexpected Codex marketplace name')
    require(len(marketplace.get('plugins', [])) == 1, 'Codex marketplace must expose exactly one plugin')
    entry = marketplace['plugins'][0]
    require(entry['name'] == 'traceary', 'unexpected Codex plugin name')
    require(entry['source']['path'] == './plugins/traceary', 'unexpected Codex plugin source path')

    plugin_manifest = read_json(ROOT / 'plugins' / 'traceary' / '.codex-plugin' / 'plugin.json')
    require(plugin_manifest['version'] == INTEGRATION_VERSION, f'Codex plugin version must track v{INTEGRATION_VERSION}')
    require(plugin_manifest.get('hooks') == './hooks.json', 'Codex plugin manifest must declare hooks: ./hooks.json so the official /plugins flow picks up Traceary hooks')

    mcp = read_json(ROOT / 'plugins' / 'traceary' / '.mcp.json')
    traceary = mcp['mcpServers']['traceary']
    require(traceary['command'] == 'traceary', 'Codex MCP must call traceary')
    require(traceary['args'] == ['mcp-server'], 'Codex MCP args must be traceary mcp-server')

    hooks = read_json(ROOT / 'plugins' / 'traceary' / 'hooks.json')
    require('SessionStart' in hooks['hooks'], 'Codex hooks must include SessionStart')
    require('UserPromptSubmit' in hooks['hooks'], 'Codex hooks must include UserPromptSubmit')
    require('Stop' in hooks['hooks'], 'Codex hooks must include Stop')
    require('PostToolUse' in hooks['hooks'], 'Codex hooks must include PostToolUse')
    require("'hook' 'session' 'codex'" in json.dumps(hooks['hooks']), 'Codex packaged hooks must invoke traceary hook session directly')
    require("'hook' 'prompt' 'codex'" in json.dumps(hooks['hooks']), 'Codex packaged hooks must invoke traceary hook prompt directly')
    require("'hook' 'audit' 'codex'" in json.dumps(hooks['hooks']), 'Codex packaged hooks must invoke traceary hook audit directly')
    require((ROOT / 'plugins' / 'traceary' / 'commands' / 'help.md').exists(), 'missing Codex help command')
    require((ROOT / 'plugins' / 'traceary' / 'commands' / 'doctor.md').exists(), 'missing Codex doctor command')
    require((ROOT / 'plugins' / 'traceary' / 'skills' / 'traceary-session-history' / 'SKILL.md').exists(), 'missing Codex traceary-session-history skill')
    require((ROOT / 'plugins' / 'traceary' / 'skills' / 'traceary-memory-review' / 'SKILL.md').exists(), 'missing Codex traceary-memory-review skill')
    require((ROOT / 'plugins' / 'traceary' / 'skills' / 'traceary-memory-remember' / 'SKILL.md').exists(), 'missing Codex traceary-memory-remember skill')
    require(not (ROOT / 'plugins' / 'traceary' / 'skills' / 'traceary-memory-capture').exists(), 'Codex traceary-memory-capture skill stub must be removed (replaced by traceary-memory-review and traceary-memory-remember)')

    # v0.14.0 retired `traceary integration codex install` (#920) and
    # v0.15.0 retired the cleanup-only `traceary integration codex
    # uninstall` (#957). Both commands are now hidden stubs that exit
    # non-zero with a usage error pointing at Codex's official
    # `/plugins` flow as the supported install/uninstall surface. The
    # smoke verifies both stubs fail fast with the right hints.
    install_result = subprocess.run(
        [
            'go',
            'run',
            '.',
            'integration',
            'codex',
            'install',
        ],
        cwd=ROOT,
        capture_output=True,
        text=True,
        env=ENGLISH_CLI_ENV,
    )
    require(install_result.returncode != 0, 'Codex install command must exit non-zero after v0.14.0 removal')
    install_message = install_result.stderr + install_result.stdout
    require('v0.14.0' in install_message, 'Codex install removal message must name v0.14.0')
    require('/plugins' in install_message, 'Codex install removal message must point at the Codex /plugins flow')

    uninstall_result = subprocess.run(
        [
            'go',
            'run',
            '.',
            'integration',
            'codex',
            'uninstall',
        ],
        cwd=ROOT,
        capture_output=True,
        text=True,
        env=ENGLISH_CLI_ENV,
    )
    require(uninstall_result.returncode != 0, 'Codex uninstall command must exit non-zero after v0.15.0 removal')
    uninstall_message = uninstall_result.stderr + uninstall_result.stdout
    require('v0.15.0' in uninstall_message, 'Codex uninstall removal message must name v0.15.0')
    require('/plugins' in uninstall_message, 'Codex uninstall removal message must point at the Codex /plugins flow')
    require('codex-plugin.md' in uninstall_message, 'Codex uninstall removal message must reference the manual cleanup guide')


def check_gemini() -> None:
    manifest = read_json(ROOT / 'integrations' / 'gemini-extension' / 'gemini-extension.json')
    require(manifest['name'] == 'traceary', 'unexpected Gemini extension name')
    require(manifest['version'] == INTEGRATION_VERSION, f'Gemini extension version must track v{INTEGRATION_VERSION}')
    traceary = manifest['mcpServers']['traceary']
    require(traceary['command'] == 'traceary', 'Gemini MCP must call traceary')
    require(traceary['args'] == ['mcp-server'], 'Gemini MCP args must be traceary mcp-server')
    require(manifest['contextFileName'] == 'GEMINI.md', 'Gemini extension must expose GEMINI.md as context file')

    hooks = read_json(ROOT / 'integrations' / 'gemini-extension' / 'hooks' / 'hooks.json')
    require('SessionStart' in hooks['hooks'], 'Gemini hooks must include SessionStart')
    require('SessionEnd' in hooks['hooks'], 'Gemini hooks must include SessionEnd')
    require('AfterTool' in hooks['hooks'], 'Gemini hooks must include AfterTool')
    require('BeforeAgent' in hooks['hooks'], 'Gemini hooks must include BeforeAgent for prompt capture')
    require('PreCompress' in hooks['hooks'], 'Gemini hooks must include PreCompress for compact_summary marker')
    require("'hook' 'session' 'gemini'" in json.dumps(hooks['hooks']), 'Gemini packaged hooks must invoke traceary hook session directly')
    require("'hook' 'audit' 'gemini'" in json.dumps(hooks['hooks']), 'Gemini packaged hooks must invoke traceary hook audit directly')
    require("'hook' 'prompt' 'gemini'" in json.dumps(hooks['hooks']), 'Gemini packaged hooks must invoke traceary hook prompt directly')
    require("'hook' 'compact' 'gemini' 'pre-compact'" in json.dumps(hooks['hooks']), 'Gemini packaged hooks must invoke traceary hook compact pre-compact directly')
    require((ROOT / 'integrations' / 'gemini-extension' / 'commands' / 'traceary-help.toml').exists(), 'missing Gemini help command')
    require((ROOT / 'integrations' / 'gemini-extension' / 'commands' / 'traceary-doctor.toml').exists(), 'missing Gemini doctor command')
    require((ROOT / 'integrations' / 'gemini-extension' / 'skills' / 'traceary-session-history' / 'SKILL.md').exists(), 'missing Gemini traceary-session-history skill')
    require((ROOT / 'integrations' / 'gemini-extension' / 'skills' / 'traceary-memory-review' / 'SKILL.md').exists(), 'missing Gemini traceary-memory-review skill')
    require((ROOT / 'integrations' / 'gemini-extension' / 'skills' / 'traceary-memory-remember' / 'SKILL.md').exists(), 'missing Gemini traceary-memory-remember skill')
    require(not (ROOT / 'integrations' / 'gemini-extension' / 'skills' / 'traceary-memory-capture').exists(), 'Gemini traceary-memory-capture skill stub must be removed (replaced by traceary-memory-review and traceary-memory-remember)')
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
