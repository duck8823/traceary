#!/usr/bin/env bash
# Select the unit-test scope for staged changes (#2341).
#
# Commit runs the tests that correspond to the change, not the full suite:
# every staged Go file maps to its owning package plus reverse dependencies,
# test-only changes map to their own package, and anything unclassifiable
# (dependency/config/infra changes, renames, deletions, unknown paths, or a
# failing package graph) expands to the full suite with a stated reason.
# Selecting zero tests is never a pass.
#
# Written for stock macOS bash 3.2: no associative arrays, no mapfile.
set -euo pipefail

MODE="staged"

usage() {
  cat <<'USAGE'
Usage: scripts/test-select-staged.sh [options]

Print the go test package list that corresponds to the current change,
one import path per line. Diagnostics and expansion reasons go to stderr.

Options:
  --staged      Use staged changes (default; what a commit records)
  --worktree    Use staged plus unstaged and untracked changes
  -h, --help    Show this help.
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --staged) MODE="staged"; shift ;;
    --worktree) MODE="worktree"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "error: unknown option $1" >&2; usage >&2; exit 64 ;;
  esac
done

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/traceary-test-select.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT
FILES="${TMP_DIR}/files"
DIRS="${TMP_DIR}/dirs"
PKGS="${TMP_DIR}/pkgs"
GRAPH="${TMP_DIR}/graph"
SELECTED="${TMP_DIR}/selected"
: > "${DIRS}"; : > "${PKGS}"; : > "${SELECTED}"

if [ "${MODE}" = "staged" ]; then
  git diff --cached --name-only > "${FILES}"
else
  { git diff --name-only; git ls-files --others --exclude-standard; } > "${FILES}" 2>/dev/null
fi

expand_all() {
  echo "expand: $1" >&2
  go list ./...
  exit 0
}

if [ ! -s "${FILES}" ]; then
  expand_all "no changed files detected"
fi

while IFS= read -r path; do
  [ -z "${path}" ] && continue
  status="$(git status --porcelain -- "${path}" | cut -c1-2)"
  case "${status}" in
    R*|D*|*D)
      expand_all "rename/delete detected: ${path}"
      ;;
  esac
  case "${path}" in
    go.mod|go.sum|go.work*|.golangci*|Makefile|.github/*|Dockerfile*|*.mk)
      expand_all "dependency/config/infra change: ${path}"
      ;;
  esac
  mapped=""
  case "${path}" in
    *.go)
      mapped="$(dirname "${path}")"
      ;;
    scripts/test-*.sh)
      mapped="scripts"
      ;;
    scripts/*.sh)
      base="$(basename "${path}" .sh)"
      if [ -f "scripts/test-${base}.sh" ]; then
        mapped="scripts"
      fi
      ;;
    docs/*|*.md|docs)
      mapped="DOCS"
      ;;
  esac
  if [ -z "${mapped}" ]; then
    expand_all "unclassifiable path: ${path}"
  fi
  printf '%s\n' "${mapped}" >> "${DIRS}"
done < "${FILES}"

sort -u "${DIRS}" -o "${DIRS}"

if grep -qx "DOCS" "${DIRS}"; then
  grep -vx "DOCS" "${DIRS}" > "${DIRS}.tmp" || true
  mv "${DIRS}.tmp" "${DIRS}"
  if [ ! -s "${DIRS}" ]; then
    echo "docs: docs-only change runs the documentation checks, not go tests" >&2
    echo "DOCS-CHECKS"
    exit 0
  fi
  echo "docs: documentation changes also present; run documentation checks alongside go tests" >&2
fi

while IFS= read -r dir; do
  pkg="$(go list "./${dir}" 2>/dev/null || true)"
  if [ -z "${pkg}" ]; then
    expand_all "package graph lookup failed for ${dir}"
  fi
  printf '%s\n' "${pkg}" >> "${PKGS}"
done < "${DIRS}"
sort -u "${PKGS}" -o "${PKGS}"

# Reverse dependencies: any package whose transitive deps include a
# changed package must also run.
if ! go list -f '{{.ImportPath}} {{join .Deps " "}}' all > "${GRAPH}" 2>/dev/null || [ ! -s "${GRAPH}" ]; then
  expand_all "package graph unavailable"
fi
cp "${PKGS}" "${SELECTED}"
while IFS= read -r pkg; do
  awk -v needle=" ${pkg} " 'index(" " $0 " ", needle) {print $1}' "${GRAPH}" >> "${SELECTED}"
done < "${PKGS}"
sort -u "${SELECTED}" -o "${SELECTED}"
cat "${SELECTED}"
