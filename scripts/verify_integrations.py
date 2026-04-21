#!/usr/bin/env python3
from __future__ import annotations

import json
import subprocess
import sys
import tempfile
from pathlib import Path

try:
    import tomllib  # type: ignore[attr-defined]
except ModuleNotFoundError:  # pragma: no cover - Python < 3.11 fallback
    tomllib = None

ROOT = Path(__file__).resolve().parent.parent
INTEGRATION_VERSION = (ROOT / 'VERSION').read_text(encoding='utf-8').strip()
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


def write_json(path: Path, payload: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, indent=2) + '\n', encoding='utf-8')


def read_toml(path: Path) -> dict:
    try:
        contents = path.read_text(encoding='utf-8')
    except FileNotFoundError:
        return {}
    if tomllib is not None:
        try:
            return tomllib.loads(contents)
        except tomllib.TOMLDecodeError as exc:
            fail(f'invalid toml in {path.relative_to(ROOT)}: {exc}')

    result: dict = {}
    current_table: list[str] = []
    for raw_line in contents.splitlines():
        line = raw_line.strip()
        if not line or line.startswith('#'):
            continue
        if line.startswith('[') and line.endswith(']'):
            header = line[1:-1]
            current = result
            current_table = []
            for part in header.split('.'):
                normalized = part.strip().strip('"\'')
                current_table.append(normalized)
                current = current.setdefault(normalized, {})
            continue
        if '=' not in line:
            continue
        key, value = [part.strip() for part in line.split('=', 1)]
        current = result
        for part in current_table:
            current = current.setdefault(part, {})
        if value in {'true', 'false'}:
            current[key] = value == 'true'
        else:
            current[key] = value.strip('"')
    return result


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
    require((ROOT / 'integrations' / 'claude-plugin' / 'skills' / 'traceary-memory-capture' / 'SKILL.md').exists(), 'missing Claude traceary-memory-capture skill')


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
    require((ROOT / 'plugins' / 'traceary' / 'skills' / 'traceary-memory-capture' / 'SKILL.md').exists(), 'missing Codex traceary-memory-capture skill')

    with tempfile.TemporaryDirectory() as temp_dir:
        temp_root = Path(temp_dir)
        codex_home = temp_root / 'codex-home'
        marketplace_root = temp_root / 'agents' / 'plugins'
        subprocess.run(
            [
                'go',
                'run',
                '.',
                'integration',
                'codex',
                'install',
                '--repo-root',
                str(ROOT),
                '--codex-home',
                str(codex_home),
                '--marketplace-root',
                str(marketplace_root),
                '--traceary-bin',
                '/tmp/custom-traceary-wrapper',
            ],
            check=True,
            cwd=ROOT,
            capture_output=True,
            text=True,
        )

        hooks_path = codex_home / 'hooks.json'
        existing_custom_hook = {
            'hooks': {
                'SessionStart': [
                    {
                        'hooks': [
                            {
                                'type': 'command',
                                'command': "custom-cli hook session codex start",
                            }
                        ]
                    }
                ],
                'PostToolUse': [
                    {
                        'matcher': '',
                        'hooks': [
                            {
                                'type': 'command',
                                'command': "custom-cli hook audit codex",
                            }
                        ],
                    }
                ],
            }
        }
        write_json(hooks_path, existing_custom_hook)

        subprocess.run(
            [
                'go',
                'run',
                '.',
                'integration',
                'codex',
                'install',
                '--repo-root',
                str(ROOT),
                '--codex-home',
                str(codex_home),
                '--marketplace-root',
                str(marketplace_root),
                '--traceary-bin',
                '/tmp/custom-traceary-wrapper',
            ],
            check=True,
            cwd=ROOT,
            capture_output=True,
            text=True,
        )

        cached_plugin_manifest = codex_home / 'plugins' / 'cache' / 'local-traceary-plugins' / 'traceary' / 'local' / '.codex-plugin' / 'plugin.json'
        require(cached_plugin_manifest.exists(), 'Codex install command must install the plugin into the active cache')

        local_config = read_toml(codex_home / 'config.toml')
        features = local_config.get('features', {})
        plugins_config = local_config.get('plugins', {})
        require(features.get('codex_hooks') is True, 'Codex install command must enable codex_hooks')
        require(
            plugins_config.get('traceary@local-traceary-plugins', {}).get('enabled') is True,
            'Codex install command must enable the Traceary plugin in config.toml',
        )

        installed_hooks = read_json(hooks_path)
        require('SessionStart' in installed_hooks['hooks'], 'Codex install command must write SessionStart hooks')
        require('UserPromptSubmit' in installed_hooks['hooks'], 'Codex install command must write UserPromptSubmit hooks')
        require('Stop' in installed_hooks['hooks'], 'Codex install command must write Stop hooks')
        require('PostToolUse' in installed_hooks['hooks'], 'Codex install command must write PostToolUse hooks')
        hook_commands = json.dumps(installed_hooks['hooks'])
        require('/tmp/custom-traceary-wrapper' in hook_commands, 'Codex install command must carry the configured traceary binary into hooks.json')
        require(("'hook' 'session' 'codex'" in hook_commands or ' hook session codex' in hook_commands), 'Codex install command must install direct hook session commands')
        require(("'hook' 'prompt' 'codex'" in hook_commands or ' hook prompt codex' in hook_commands), 'Codex install command must install direct hook prompt commands')
        require(("'hook' 'audit' 'codex'" in hook_commands or ' hook audit codex' in hook_commands), 'Codex install command must install direct hook audit commands')
        require('custom-cli hook session codex start' in hook_commands, 'Codex install command must preserve unrelated session hooks')
        require('custom-cli hook audit codex' in hook_commands, 'Codex install command must preserve unrelated audit hooks')
        require('traceary-session-start' in hook_commands, 'Codex install command must name managed session-start hooks')
        require('traceary-session-stop' in hook_commands, 'Codex install command must name managed session-stop hooks')
        require('traceary-audit' in hook_commands, 'Codex install command must name managed audit hooks')

        config_path = codex_home / 'config.toml'
        config_path.write_text(
            config_path.read_text(encoding='utf-8')
            + '\n'
            + '[plugins."traceary@local-traceary-plugins".auth]\n'
            + 'provider = "local"\n'
            + '\n'
            + '[plugins."other-plugin"]\n'
            + 'enabled = true\n',
            encoding='utf-8',
        )

        subprocess.run(
            [
                'go',
                'run',
                '.',
                'integration',
                'codex',
                'uninstall',
                '--codex-home',
                str(codex_home),
                '--marketplace-root',
                str(marketplace_root),
            ],
            check=True,
            cwd=ROOT,
            capture_output=True,
            text=True,
        )

        require(not cached_plugin_manifest.exists(), 'Codex uninstall command must remove the cached plugin')
        local_config = read_toml(codex_home / 'config.toml')
        require(
            'traceary@local-traceary-plugins' not in local_config.get('plugins', {}),
            'Codex uninstall command must remove the Traceary plugin config entry',
        )
        require(
            local_config.get('plugins', {}).get('other-plugin', {}).get('enabled') is True,
            'Codex uninstall command must preserve unrelated plugin config entries',
        )
        if hooks_path.exists():
            remaining_hooks = json.dumps(read_json(hooks_path))
            require('traceary-session.sh' not in remaining_hooks, 'Codex uninstall command must remove Traceary session hooks')
            require('traceary-audit.sh' not in remaining_hooks, 'Codex uninstall command must remove Traceary audit hooks')
            require('/tmp/custom-traceary-wrapper' not in remaining_hooks, 'Codex uninstall command must remove direct Traceary hooks that use the configured traceary binary')
            require('custom-cli hook session codex start' in remaining_hooks, 'Codex uninstall command must preserve unrelated session hooks')
            require('custom-cli hook audit codex' in remaining_hooks, 'Codex uninstall command must preserve unrelated audit hooks')


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
    require("'hook' 'session' 'gemini'" in json.dumps(hooks['hooks']), 'Gemini packaged hooks must invoke traceary hook session directly')
    require("'hook' 'audit' 'gemini'" in json.dumps(hooks['hooks']), 'Gemini packaged hooks must invoke traceary hook audit directly')
    require((ROOT / 'integrations' / 'gemini-extension' / 'commands' / 'traceary-help.toml').exists(), 'missing Gemini help command')
    require((ROOT / 'integrations' / 'gemini-extension' / 'commands' / 'traceary-doctor.toml').exists(), 'missing Gemini doctor command')
    require((ROOT / 'integrations' / 'gemini-extension' / 'skills' / 'traceary-session-history' / 'SKILL.md').exists(), 'missing Gemini traceary-session-history skill')
    require((ROOT / 'integrations' / 'gemini-extension' / 'skills' / 'traceary-memory-capture' / 'SKILL.md').exists(), 'missing Gemini traceary-memory-capture skill')
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
