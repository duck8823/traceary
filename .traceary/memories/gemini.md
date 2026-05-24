<!-- traceary-memories:begin:v1 -->
<!-- DO NOT EDIT: this block is managed by Traceary. Hand edits will be overwritten by `traceary memory export` or `traceary memory activate`. -->

# Traceary-managed gemini memories

## Workspace memories: github.com/duck8823/traceary

### Constraints

- v0.8 の新 hook (transcript / built-in tool matcher) を実環境で効かせるには、brew upgrade traceary + ~/.claude/plugins/marketplaces/traceary-plugins の git pull + claude plugins update が必要。片方だけでは古い hooks.json が残る (memory_id: memory-825f38352ae0d99128bab1162c33e9d7, scope: workspace=github.com/duck8823/traceary)

### Lessons

- Codex GitHub reviews can take several minutes to complete; keep polling PR review/check state instead of assuming a missing response means the review failed or should be skipped. (memory_id: memory-beb524035b13bf7ea877001a36b8ccbc, scope: workspace=github.com/duck8823/traceary)
- replay UI の HTML は GitHub で render できないので、Markdown format の追加が必要。follow-up で format フラグを検討 (memory_id: memory-9663a7faa775387ea03aca34892284c2, scope: workspace=github.com/duck8823/traceary)

<!-- traceary-memories:end -->
