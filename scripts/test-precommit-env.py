#!/usr/bin/env python3
"""Hermetic pre-commit contract: Git context is retained for selection, not tests."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

HOOK = Path(__file__).resolve().parent / "githooks/pre-commit"


class PreCommitEnvironmentTest(unittest.TestCase):
    def run_hook(self, *, linked=False, test_exit=0, format_fail=False):
        with tempfile.TemporaryDirectory(prefix="traceary-precommit-") as temp:
            root = Path(temp)
            bin_dir = root / "bin"
            bin_dir.mkdir()
            (root / "scripts").mkdir()
            scripts = {
                bin_dir / "git": '\n'.join([
                    '#!/usr/bin/env bash',
                    'case "$*" in',
                    ' "rev-parse --show-toplevel") printf "%s\\n" "$TEST_ROOT" ;;',
                    ' "rev-parse --local-env-vars") printf "%s\\n" GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_PREFIX ;;',
                    ' "diff --cached --name-only --diff-filter=ACM") [ "$GIT_DIR" = "$EXPECTED_GIT_DIR" ] || exit 95; printf "fixture.go\\n" ;;',
                    ' *) exit 91 ;;',
                    'esac',
                ]),
                root / "scripts/test-select-staged.sh": '#!/usr/bin/env bash\n[ "$GIT_DIR" = "$EXPECTED_GIT_DIR" ] || exit 92\nprintf "example.test/selected\\n"\n',
                bin_dir / "go": '#!/usr/bin/env bash\n[ "$*" = "test example.test/selected" ] || exit 93\nfor key in GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_PREFIX; do [ -z "${!key+x}" ] || exit 94; done\nprintf called > "$TEST_ROOT/go-called"\nexit "$TEST_EXIT"\n',
                bin_dir / "gofmt": '#!/usr/bin/env bash\n[ "$GIT_DIR" = "$EXPECTED_GIT_DIR" ] || exit 96\n[ "$FORMAT_FAIL" = 0 ] || printf "fixture.go\\n"\n',
            }
            for path, content in scripts.items():
                path.write_text(content + "\n")
                path.chmod(0o755)
            git_dir = str(root / ("common.git/worktrees/linked" if linked else ".git"))
            env = os.environ | {
                "PATH": str(bin_dir) + ":/usr/bin:/bin", "TEST_ROOT": str(root),
                "GIT_DIR": git_dir, "EXPECTED_GIT_DIR": git_dir,
                "GIT_WORK_TREE": str(root), "GIT_INDEX_FILE": str(root / "index"),
                "GIT_PREFIX": "nested/", "TRACEARY_SKIP_HOOKS": "0",
                "TEST_EXIT": str(test_exit), "FORMAT_FAIL": str(int(format_fail)),
            }
            result = subprocess.run(["bash", str(HOOK)], env=env, text=True, capture_output=True)
            return result, (root / "go-called").exists()

    def test_normal_checkout_clears_only_test_git_context(self):
        result, called = self.run_hook()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(called)

    def test_linked_worktree_clears_only_test_git_context(self):
        result, called = self.run_hook(linked=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(called)

    def test_test_failure_still_blocks_commit(self):
        result, called = self.run_hook(test_exit=23)
        self.assertTrue(called)
        self.assertEqual(result.returncode, 23)

    def test_format_failure_still_blocks_commit(self):
        result, called = self.run_hook(format_fail=True)
        self.assertTrue(called)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unformatted Go files", result.stderr)


if __name__ == "__main__":
    unittest.main()
