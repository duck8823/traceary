#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import shlex
import shutil
from pathlib import Path

PLUGIN_NAME = 'traceary'
MARKETPLACE_NAME = 'local-traceary-plugins'
PLUGIN_ID = f'{PLUGIN_NAME}@{MARKETPLACE_NAME}'
TRACEARY_CODEX_HOOK_NAMES = {
    'traceary-session-start',
    'traceary-session-stop',
    'traceary-audit',
}


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


def parse_toml_header(line: str) -> tuple[str, bool] | None:
    stripped = line.strip()
    if stripped.startswith('[[') and stripped.endswith(']]'):
        return stripped[2:-2].strip(), True
    if stripped.startswith('[') and stripped.endswith(']'):
        return stripped[1:-1].strip(), False
    return None


def is_same_or_descendant_table(header_name: str, candidate_name: str) -> bool:
    return candidate_name == header_name or candidate_name.startswith(f'{header_name}.')


def find_table_bounds(lines: list[str], header: str) -> tuple[int, int] | None:
    parsed_header = parse_toml_header(header)
    if parsed_header is None:
        raise ValueError(f'invalid toml header: {header}')
    header_name, _ = parsed_header

    start = None
    for index, line in enumerate(lines):
        if line.strip() == header:
            start = index
            break
    if start is None:
        return None
    end = len(lines)
    for index in range(start + 1, len(lines)):
        parsed_candidate = parse_toml_header(lines[index])
        if parsed_candidate is None:
            continue
        candidate_name, _ = parsed_candidate
        if not is_same_or_descendant_table(header_name, candidate_name):
            end = index
            break
    return start, end


def normalize_toml_text(lines: list[str]) -> str:
    collapsed: list[str] = []
    previous_blank = False
    for line in lines:
        current_blank = not line.strip()
        if current_blank and previous_blank:
            continue
        collapsed.append(line)
        previous_blank = current_blank
    while collapsed and not collapsed[-1].strip():
        collapsed.pop()
    return ('\n'.join(collapsed) + '\n') if collapsed else ''


def remove_toml_table(text: str, header: str) -> str:
    lines = text.splitlines()
    bounds = find_table_bounds(lines, header)
    if bounds is None:
        return normalize_toml_text(lines)
    start, end = bounds
    del lines[start:end]
    return normalize_toml_text(lines)


def is_legacy_traceary_binary(token: str) -> bool:
    """Return true for pre-name direct hooks that used the default `traceary` basename."""
    return Path(token.strip()).name == 'traceary'


def is_traceary_script(token: str, script_name: str) -> bool:
    return Path(token.strip()).name == script_name


def is_traceary_codex_session_hook(tokens: list[str], index: int) -> bool:
    return len(tokens) == index + 3 and tokens[index + 1] == 'codex' and tokens[index + 2] in {'start', 'stop'}


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


def is_traceary_codex_hook(hook: dict) -> bool:
    if hook.get('name') in TRACEARY_CODEX_HOOK_NAMES:
        return True

    command = str(hook.get('command', ''))
    try:
        tokens = shlex.split(command)
    except ValueError:
        return False
    return is_traceary_codex_script_hook(tokens) or is_legacy_traceary_codex_direct_hook(tokens)


def remove_traceary_hooks(hooks_path: Path) -> bool:
    if not hooks_path.exists():
        return False
    payload = json.loads(hooks_path.read_text(encoding='utf-8'))
    hooks = payload.get('hooks', {})
    for event_name, matchers in list(hooks.items()):
        cleaned_matchers = []
        for matcher in matchers:
            matcher_hooks = matcher.get('hooks', [])
            remaining = [hook for hook in matcher_hooks if not is_traceary_codex_hook(hook)]
            if remaining:
                updated = dict(matcher)
                updated['hooks'] = remaining
                cleaned_matchers.append(updated)
        if cleaned_matchers:
            hooks[event_name] = cleaned_matchers
        else:
            hooks.pop(event_name, None)
    payload['hooks'] = hooks
    if not hooks:
        hooks_path.unlink()
        return True
    write_json(hooks_path, payload)
    return True


def main() -> None:
    parser = argparse.ArgumentParser(description='Remove the packaged Traceary Codex plugin from the active Codex runtime and local marketplace.')
    parser.add_argument('--codex-home', type=Path, default=Path.home() / '.codex')
    parser.add_argument('--marketplace-root', type=Path, default=Path.home() / '.agents' / 'plugins')
    args = parser.parse_args()

    codex_home = args.codex_home.expanduser().resolve()
    marketplace_root = args.marketplace_root.expanduser().resolve()

    marketplace_plugin = marketplace_root / 'plugins' / PLUGIN_NAME
    marketplace_path = marketplace_root / 'marketplace.json'
    plugin_cache_root = codex_home / 'plugins' / 'cache' / MARKETPLACE_NAME / PLUGIN_NAME
    config_path = codex_home / 'config.toml'
    hooks_path = codex_home / 'hooks.json'

    if marketplace_plugin.exists():
        shutil.rmtree(marketplace_plugin)
        print(f'removed marketplace copy {marketplace_plugin}')
    else:
        print(f'marketplace copy already absent: {marketplace_plugin}')

    if marketplace_path.exists():
        marketplace = load_marketplace(marketplace_path)
        marketplace['plugins'] = [entry for entry in marketplace.get('plugins', []) if entry.get('name') != PLUGIN_NAME]
        write_json(marketplace_path, marketplace)
        print(f'updated marketplace manifest at {marketplace_path}')

    if plugin_cache_root.exists():
        shutil.rmtree(plugin_cache_root)
        print(f'removed active Codex plugin cache {plugin_cache_root}')
    else:
        print(f'plugin cache already absent: {plugin_cache_root}')

    if config_path.exists():
        updated = remove_toml_table(config_path.read_text(encoding='utf-8'), f'[plugins."{PLUGIN_ID}"]')
        config_path.write_text(updated, encoding='utf-8')
        print(f'updated Codex config at {config_path}')

    if remove_traceary_hooks(hooks_path):
        print(f'removed Traceary Codex hooks from {hooks_path}')
    else:
        print(f'Codex hooks already absent: {hooks_path}')

    print('left [features].codex_hooks unchanged so other local hook workflows keep working')


if __name__ == '__main__':
    main()
