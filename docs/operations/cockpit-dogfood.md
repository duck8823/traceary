# Cockpit dogfood checklist

[日本語](./cockpit-dogfood.ja.md)

Use this checklist before cutting a release that changes `traceary tui` / the operator cockpit. It is intentionally task-oriented: the goal is to catch flows that are technically wired but confusing in practice.

## Automated coverage

Run:

```sh
go test ./presentation/cli -run 'TestCockpitDogfood'
```

The dogfood tests cover:

- Home all-green state.
- Home doctor failure state.
- Home candidate-memory notifications.
- Home stale-session warnings.
- Home new events plus recent failures.
- An ambiguous memory candidate where the expected action is `edit/distill` or `skip`, not a one-tap accept.
- Representative terminal sizes: 80x24, 120x32, and 160x40.
- Keyboard paths for:
  - finding the latest failure from Home via Live and event detail,
  - inspecting evidence for an ambiguous memory without accidentally accepting it,
  - opening Doctor and finding a remediation command.
- Japanese cockpit smoke coverage with `TRACEARY_LANG=ja` at 80x24.
- Settings coverage for switching `ui.language`, cycling one safe read setting, and validating redaction regex input before save.

Golden snapshots live under `presentation/cli/testdata/cockpit/`. Update them only when the intended cockpit copy/layout changes:

```sh
TRACEARY_UPDATE_GOLDEN=1 go test ./presentation/cli -run TestCockpitDogfoodGoldenSnapshots
```

## Manual release-prep smoke

Run each task in a real terminal before tagging a cockpit release:

1. `traceary tui --reset-state`
   - Confirm Home opens directly to the triage board.
   - Confirm the footer explains global navigation and help.
2. Resize to 80x24, 120x32, and 160x40.
   - Confirm the section bar remains understandable.
   - Confirm the most important card still shows a `next:` action without needing command memorization.
3. Press `?` on Home.
   - Confirm the action menu explains Live, Doctor, Memory, and Sessions.
4. Press `2` for Live.
   - Confirm event rows are scannable.
   - Select an event and press Enter; `Esc` should return to Live without quitting.
5. Press `4` for Memory.
   - Find an ambiguous or low-confidence candidate.
   - Confirm the UI suggests edit/distill or skip, and that one `a` press does not accept weak candidates.
6. Press `3` for Doctor.
   - Confirm warnings/failures show remediation commands inline.
7. Press `5` for Sessions.
   - Confirm the screen points to handoff/session commands until the dedicated session UI is implemented.
8. Run `TRACEARY_LANG=ja traceary tui --reset-state`.
   - Confirm the shell, footer, help/action menu, Home labels, and Memory review decision aids are understandable in Japanese.
   - Confirm literal commands such as `traceary doctor --json` remain copyable as English command names.
9. Press `6` for Settings.
   - Confirm the config path/status, `TRACEARY_LANG` override explanation, environment diagnostics, and read-only preset/rule lists are visible.
   - Stage `ui.language` from English to Japanese and back, cycle `read.color` or `read.fields`, then review the diff before confirming save.
   - Try an invalid `redact.extra_patterns` regex and confirm it is rejected before any config write.

## Release gate

Do not tag the release if any of these are true:

- A main task cannot be discovered from Home or `?` help.
- A memory candidate can be accepted accidentally without enough context.
- New memory/event state is ambiguous after mark-seen behavior.
- Doctor failures do not show a concrete next action.
- The 80x24 smoke hides the primary action for the highest-priority card.
