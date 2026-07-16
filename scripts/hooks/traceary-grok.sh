#!/bin/sh
set -eu

action="${1:?missing Grok hook action}"
case "${action}" in
  session-start|user-prompt-submit|pre-tool-use|post-tool-use|stop|pre-compact|post-compact) ;;
  *)
    printf 'unsupported Grok hook action: %s\n' "${action}" >&2
    exit 64
    ;;
esac

exec traceary hook grok "${action}"
