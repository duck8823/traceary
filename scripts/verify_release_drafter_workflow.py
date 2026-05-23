#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
WORKFLOW = ROOT / '.github' / 'workflows' / 'release-drafter.yml'


def fail(message: str) -> None:
    raise SystemExit(f'error: {message}')


def require(condition: bool, message: str) -> None:
    if not condition:
        fail(message)


def autolabel_should_run(head_ref: str) -> bool:
    """Mirror the release-drafter autolabel job guard for branch fixtures."""

    return not head_ref.startswith('maintenance/homebrew-v')


def main() -> None:
    workflow = WORKFLOW.read_text(encoding='utf-8')
    require(
        "!startsWith(github.head_ref, 'maintenance/homebrew-v')" in workflow,
        'release-drafter autolabel must skip release-bot Homebrew formula PRs',
    )
    require(
        'short-lived GitHub App token' in workflow
        and 'not\n    # inputs to release-note classification' in workflow,
        'release-drafter autolabel skip rationale must be documented in workflow comments',
    )
    require(
        'issues: write' in workflow,
        'release-drafter autolabel needs issues: write because PR labels use the issues API',
    )

    branch_expectations = {
        'maintenance/homebrew-v0.17.0': False,
        'maintenance/homebrew-v1.2.3': False,
        'maintenance/v0.17-10-release-autolabel': True,
        'feature/cockpit-follow-up': True,
        'fix/release-drafter': True,
        'dependabot/go_modules/dependencies': True,
    }
    for branch, expected in branch_expectations.items():
        actual = autolabel_should_run(branch)
        require(
            actual == expected,
            f'autolabel branch guard mismatch for {branch}: got {actual}, want {expected}',
        )

    print('ok: release-drafter workflow autolabel guard is documented and verified')


if __name__ == '__main__':
    main()
