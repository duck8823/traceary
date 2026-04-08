#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import shutil
from pathlib import Path

PLUGIN_NAME = 'traceary'
ROOT = Path(__file__).resolve().parents[2]


def main() -> None:
    parser = argparse.ArgumentParser(description='Remove the packaged Traceary Codex plugin from a local marketplace directory.')
    parser.add_argument('--target-root', type=Path, default=Path.home() / '.agents' / 'plugins')
    args = parser.parse_args()

    target_root = args.target_root.expanduser().resolve()
    target_plugin = target_root / 'plugins' / PLUGIN_NAME
    marketplace_path = target_root / 'marketplace.json'

    if target_plugin.exists():
        shutil.rmtree(target_plugin)
        print(f'removed {target_plugin}')
    else:
        print(f'plugin directory already absent: {target_plugin}')

    if marketplace_path.exists():
        marketplace = json.loads(marketplace_path.read_text(encoding='utf-8'))
        marketplace['plugins'] = [entry for entry in marketplace.get('plugins', []) if entry.get('name') != PLUGIN_NAME]
        marketplace_path.write_text(json.dumps(marketplace, indent=2) + '\n', encoding='utf-8')
        print(f'updated marketplace manifest at {marketplace_path}')


if __name__ == '__main__':
    main()
