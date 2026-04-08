#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
PLUGIN_NAME = 'traceary'


def load_marketplace(path: Path) -> dict:
    if not path.exists():
        return {
            'name': 'local-traceary-plugins',
            'interface': {'displayName': 'Local Traceary Plugins'},
            'plugins': [],
        }
    return json.loads(path.read_text(encoding='utf-8'))


def main() -> None:
    parser = argparse.ArgumentParser(description='Install the packaged Traceary Codex plugin into a local marketplace directory.')
    parser.add_argument('--repo-root', type=Path, default=ROOT)
    parser.add_argument('--target-root', type=Path, default=Path.home() / '.agents' / 'plugins')
    args = parser.parse_args()

    source_plugin = args.repo_root / 'plugins' / PLUGIN_NAME
    target_root = args.target_root.expanduser().resolve()
    target_plugins = target_root / 'plugins'
    target_plugin = target_plugins / PLUGIN_NAME
    marketplace_path = target_root / 'marketplace.json'

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
    marketplace_path.write_text(json.dumps(marketplace, indent=2) + '\n', encoding='utf-8')

    print(f'installed {PLUGIN_NAME} plugin at {target_plugin}')
    print(f'updated marketplace manifest at {marketplace_path}')


if __name__ == '__main__':
    main()
