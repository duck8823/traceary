#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import shlex
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
PLUGIN_NAME = 'traceary'
MARKETPLACE_NAME = 'local-traceary-plugins'
PLUGIN_ID = f'{PLUGIN_NAME}@{MARKETPLACE_NAME}'
TRACEARY_CODEX_HOOK_NAMES = {
    'traceary-session-start',
    'traceary-session-stop',
    'traceary-audit',
}
PLUGIN_VERSION = 'local'


def load_marketplace(path: Path) -> dict:
    if not path.exists():
        return {
            'name': MARKETPLACE_NAME,
            'interface': {'displayName': 'Local Traceary Plugins'},
            'plugins': [],
        }
    return json.loads(path.read_text(encoding='utf-8'))


def write_json(path: Path, payload: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, indent=2) + '\n', encoding='utf-8')


def install_marketplace_copy(source_plugin: Path, marketplace_root: Path) -> Path:
    target_plugins = marketplace_root / 'plugins'
    target_plugin = target_plugins / PLUGIN_NAME
    marketplace_path = marketplace_root / 'marketplace.json'

    target_plugins.mkdir(parents=True, exist_ok=True)
    if target_plugin.exists():
        shutil.rmtree(target_plugin)
    shutil.copytree(source_plugin, target_plugin)

    marketplace = load_marketplace(marketplace_path)
    plugins = [entry for entry in marketplace.get('plugins', []) if entry.get('name') != PLUGIN_NAME]
    plugins.append(
        {
            'name': PLUGIN_NAME,
            'source': {'source': 'local', 'path': f'./plugins/{PLUGIN_NAME}'},
            'policy': {'installation': 'AVAILABLE', 'authentication': 'ON_INSTALL'},
            'category': 'Coding',
        }
    )
    marketplace['plugins'] = plugins
    write_json(marketplace_path, marketplace)
    return target_plugin


def install_plugin_cache_copy(source_plugin: Path, codex_home: Path) -> Path:
    plugin_root = codex_home / 'plugins' / 'cache' / MARKETPLACE_NAME / PLUGIN_NAME / PLUGIN_VERSION
    base_root = plugin_root.parent
    if base_root.exists():
        shutil.rmtree(base_root)
    plugin_root.parent.mkdir(parents=True, exist_ok=True)
    shutil.copytree(source_plugin, plugin_root)
    return plugin_root


def find_table_bounds(lines: list[str], header: str) -> tuple[int, int] | None:
    start = None
    for index, line in enumerate(lines):
        if line.strip() == header:
            start = index
            break
    if start is None:
        return None
    end = len(lines)
    for index in range(start + 1, len(lines)):
        stripped = lines[index].strip()
        if stripped.startswith('[') and stripped.endswith(']'):
            end = index
            break
    return start, end


def normalize_toml_text(lines: list[str]) -> str:
    while lines and not lines[-1].strip():
        lines.pop()
    return ('\n'.join(lines) + '\n') if lines else ''


def upsert_toml_bool(text: str, header: str, key: str, value: bool) -> str:
    lines = text.splitlines()
    bounds = find_table_bounds(lines, header)
    rendered_value = 'true' if value else 'false'
    setting = f'{key} = {rendered_value}'

    if bounds is None:
        if lines and lines[-1].strip():
            lines.append('')
        lines.extend([header, setting])
        return normalize_toml_text(lines)

    start, end = bounds
    for index in range(start + 1, end):
        stripped = lines[index].strip()
        if stripped == key or stripped.startswith(f'{key} =') or stripped.startswith(f'{key}='):
            lines[index] = setting
            return normalize_toml_text(lines)

    lines.insert(end, setting)
    return normalize_toml_text(lines)


def write_codex_config(codex_home: Path) -> Path:
    config_path = codex_home / 'config.toml'
    current = config_path.read_text(encoding='utf-8') if config_path.exists() else ''
    current = upsert_toml_bool(current, '[features]', 'codex_hooks', True)
    current = upsert_toml_bool(current, f'[plugins."{PLUGIN_ID}"]', 'enabled', True)
    config_path.parent.mkdir(parents=True, exist_ok=True)
    config_path.write_text(current, encoding='utf-8')
    return config_path


def shell_quote(value: str) -> str:
    return "'" + value.replace("'", "'\"'\"'") + "'"


def build_hook_command(traceary_bin: str, *args: str) -> str:
    parts = [shell_quote(traceary_bin)]
    parts.extend(shell_quote(arg) for arg in args)
    return ' '.join(parts)


def build_codex_hooks(traceary_bin: str) -> dict:
    empty_matcher = ''
    return {
        'SessionStart': [
            {
                'hooks': [
                    {
                        'name': 'traceary-session-start',
                        'type': 'command',
                        'command': build_hook_command(traceary_bin, 'hook', 'session', 'codex', 'start'),
                    }
                ]
            }
        ],
        'Stop': [
            {
                'hooks': [
                    {
                        'name': 'traceary-session-stop',
                        'type': 'command',
                        'command': build_hook_command(traceary_bin, 'hook', 'session', 'codex', 'stop'),
                    }
                ]
            }
        ],
        'PostToolUse': [
            {
                'matcher': empty_matcher,
                'hooks': [
                    {
                        'name': 'traceary-audit',
                        'type': 'command',
                        'command': build_hook_command(traceary_bin, 'hook', 'audit', 'codex'),
                    }
                ],
            }
        ],
    }


def is_traceary_binary(token: str, traceary_bin: str) -> bool:
    return token.strip() == traceary_bin.strip()


def is_legacy_traceary_binary(token: str) -> bool:
    """Return true for pre-name direct hooks that used the default `traceary` basename."""
    return Path(token.strip()).name == 'traceary'


def is_traceary_script(token: str, script_name: str) -> bool:
    return Path(token.strip()).name == script_name


def is_traceary_codex_session_hook(tokens: list[str], index: int) -> bool:
    return len(tokens) == index + 3 and tokens[index + 1] == 'codex' and tokens[index + 2] in {'start', 'stop'}


def is_traceary_codex_direct_hook(tokens: list[str], traceary_bin: str) -> bool:
    if len(tokens) == 5 and is_traceary_binary(tokens[0], traceary_bin):
        return tokens[1] == 'hook' and tokens[2] == 'session' and tokens[3] == 'codex' and tokens[4] in {'start', 'stop'}
    if len(tokens) == 4 and is_traceary_binary(tokens[0], traceary_bin):
        return tokens[1] == 'hook' and tokens[2] == 'audit' and tokens[3] == 'codex'
    return False


def is_legacy_traceary_codex_direct_hook(tokens: list[str]) -> bool:
    """Match legacy direct hooks that predate explicit Traceary hook names."""
    if len(tokens) == 5 and is_legacy_traceary_binary(tokens[0]):
        return tokens[1] == 'hook' and tokens[2] == 'session' and tokens[3] == 'codex' and tokens[4] in {'start', 'stop'}
    if len(tokens) == 4 and is_legacy_traceary_binary(tokens[0]):
        return tokens[1] == 'hook' and tokens[2] == 'audit' and tokens[3] == 'codex'
    return False


def is_traceary_codex_script_hook(tokens: list[str]) -> bool:
    for index, token in enumerate(tokens):
        if is_traceary_script(token, 'traceary-session.sh') and is_traceary_codex_session_hook(tokens, index):
            return True
        if is_traceary_script(token, 'traceary-audit.sh') and len(tokens) == index + 2 and tokens[index + 1] == 'codex':
            return True
    return False


def is_traceary_codex_hook(hook: dict, traceary_bin: str) -> bool:
    # Explicit Traceary hook names are the stable primary marker for direct hooks.
    if hook.get('name') in TRACEARY_CODEX_HOOK_NAMES:
        return True

    command = str(hook.get('command', ''))
    try:
        tokens = shlex.split(command)
    except ValueError:
        return False
    return (
        is_traceary_codex_script_hook(tokens)
        or is_traceary_codex_direct_hook(tokens, traceary_bin)
        or is_legacy_traceary_codex_direct_hook(tokens)
    )


def merge_codex_hooks(hooks_path: Path, traceary_bin: str) -> Path:
    existing = {'hooks': {}}
    if hooks_path.exists():
        existing = json.loads(hooks_path.read_text(encoding='utf-8'))
    hooks = existing.setdefault('hooks', {})
    generated = build_codex_hooks(traceary_bin)

    for event_name, matchers in generated.items():
        cleaned_matchers = []
        for matcher in hooks.get(event_name, []):
            matcher_hooks = matcher.get('hooks', [])
            remaining = [hook for hook in matcher_hooks if not is_traceary_codex_hook(hook, traceary_bin)]
            if remaining:
                updated = dict(matcher)
                updated['hooks'] = remaining
                cleaned_matchers.append(updated)
        cleaned_matchers.extend(matchers)
        hooks[event_name] = cleaned_matchers

    write_json(hooks_path, existing)
    return hooks_path


def main() -> None:
    parser = argparse.ArgumentParser(description='Install the packaged Traceary Codex plugin into the active Codex runtime and local marketplace.')
    parser.add_argument('--repo-root', type=Path, default=ROOT)
    parser.add_argument('--codex-home', type=Path, default=Path.home() / '.codex')
    parser.add_argument('--marketplace-root', type=Path, default=Path.home() / '.agents' / 'plugins')
    parser.add_argument('--traceary-bin', default='traceary')
    args = parser.parse_args()

    source_plugin = args.repo_root.expanduser().resolve() / 'plugins' / PLUGIN_NAME
    codex_home = args.codex_home.expanduser().resolve()
    marketplace_root = args.marketplace_root.expanduser().resolve()
    traceary_bin = args.traceary_bin

    marketplace_copy = install_marketplace_copy(source_plugin, marketplace_root)
    installed_plugin_root = install_plugin_cache_copy(source_plugin, codex_home)
    config_path = write_codex_config(codex_home)
    hooks_path = merge_codex_hooks(codex_home / 'hooks.json', traceary_bin)

    print(f'installed marketplace copy at {marketplace_copy}')
    print(f'installed active Codex plugin at {installed_plugin_root}')
    print(f'updated Codex config at {config_path}')
    print(f'updated Codex hooks at {hooks_path}')
    print(f'enabled plugin id {PLUGIN_ID}')


if __name__ == '__main__':
    main()
