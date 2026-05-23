# Cockpit UI/UX design baseline (v0.18)

[日本語](./cockpit-ui-ux-design.ja.md)

This document is the design baseline for issue #1030 and the v0.18 cockpit usability overhaul. It intentionally precedes implementation. The v0.17 cockpit collected the right surfaces (`top`, `tail`, `doctor`, `memory inbox review`), but the interaction model is not yet good enough for daily use.

## User feedback

`traceary tui` is too hard to use. The issue is not the value of the underlying data; it is that the TUI does not yet explain itself, does not keep navigation stable, and forces the operator to remember hidden mode-specific keys.

## Design principles

1. **One cockpit, not a menu of commands.** The TUI should feel like a persistent operator console. Operators should not have to remember `top`, `tail`, `doctor`, or `memory inbox review` while inside it.
2. **Visible orientation at all times.** Every screen must answer: where am I, what is selected, what changed, and what can I do next?
3. **Actions are discoverable before they are memorized.** Single-key shortcuts remain, but the UI must expose a contextual action list and `?` help for the current screen.
4. **Home is a triage board.** The home screen should prioritize what needs attention, not only print counts.
5. **Keep CLI escape hatches.** Direct subcommands remain stable for scripts, dedicated terminal panes, and power users.
6. **No destructive magic.** Any action that changes data, such as `memory inbox accept` / `memory inbox reject` decisions or session GC, must be explicit and reversible where possible.

## Reference patterns

These are references, not clone targets.

| Product / guide | Pattern to adopt | Pattern to avoid |
|---|---|---|
| lazygit | Persistent panels, always-available `?`, context-specific key hints, `Tab`/number navigation, `/` filter | Git-specific mental model, too many dense shortcuts on first screen |
| k9s | Command/filter mode, `?` help, quick view switching, predictable Esc/cancel behavior | Kubernetes-specific resource grammar, destructive one-key actions |
| btop / htop | Glanceable status regions, stable screen zones, obvious selected row | Dense telemetry wall without next actions |
| Apple HIG keyboard guidance | Respect common keyboard expectations, avoid surprising shortcuts, support keyboard-only operation | Repurposing common keys differently by screen |
| Material accessibility guidance | Clear focus, state, and feedback; navigation/menu labels should describe the action | Hidden focus and ambiguous state changes |
| Bubble Tea | Keep model/update/view explicit; use small composable state machines for modes | One large model where screen-specific behavior leaks everywhere |

Reference links:

- lazygit keybindings: https://github.com/jesseduffield/lazygit/blob/master/docs/keybindings/Keybindings_en.md
- lazygit configuration/keybinding reference: https://github.com/jesseduffield/lazygit/blob/master/docs/Config.md
- k9s commands/key bindings: https://k9scli.io/topics/commands/
- btop interactive tutorial/key bindings: https://btop.one/
- Apple keyboard HIG: https://developer.apple.com/design/human-interface-guidelines/keyboards/
- Material accessibility: https://m2.material.io/design/usability/accessibility.html
- Bubble Tea: https://github.com/charmbracelet/bubbletea

## Current cockpit audit

Current implementation inspected: `presentation/cli/cockpit.go` and shared TUI primitives under `presentation/cli/tui/`.

### What works

- The explicit entrypoint (`traceary tui`) is correct; bare `traceary` should still show CLI help.
- The first set of surfaces exists: home, live tail, doctor, event detail, memory review.
- The cockpit can surface new memory and event counts using local last-seen state.
- Existing direct commands remain available for scripts and fallback.

### Main usability failures

| Failure | Current behavior | Why it hurts |
|---|---|---|
| Hidden navigation model | Home uses `d`, `t`/`l`, `m`; other screens use `h`; memory review has its own nested keys | The operator must remember modes instead of following visible UI structure |
| No persistent shell | Each mode renders its own title/footer | Location and global actions are not stable across screens |
| Home is a count dump | `ATTENTION`, `OVERVIEW`, and `Cockpit surfaces` are text sections | Counts do not clearly map to next actions or workflow priority |
| Help is fallback-command oriented | `?` on home lists CLI fallback commands | Help does not teach the current TUI interaction model |
| Esc semantics conflict | Shared keymap binds `esc` to quit; many TUIs use Esc for back/cancel | Accidental exit risk and poor transfer from lazygit/k9s-style TUIs |
| Live tail lacks filter/search affordance | `/` exists in shared keymap but cockpit live does not use it | Operators cannot narrow noisy event streams inside the cockpit |
| Doctor pane is read-only text | Fix commands are shown, but no action menu or copy/run affordance | It is not obvious whether the cockpit can help remediate |
| Detail screen loses origin context | Detail can go home or live, but the breadcrumb is minimal | Users can forget what list/detail relationship they are in |

## Target information architecture

The cockpit becomes a tabbed operator console with persistent global chrome.

```text
┌ Traceary cockpit ─ workspace=github.com/duck8823/traceary ─ db=... ─ 12:34:56 ┐
│ [1 Home] [2 Live] [3 Health] [4 Memory] [5 Sessions]                         │
├───────────────────────────────────────────────────────────────────────────────┤
│ screen-specific content                                                       │
├───────────────────────────────────────────────────────────────────────────────┤
│ ? help  : actions  / filter  tab next  esc back  q quit                       │
└───────────────────────────────────────────────────────────────────────────────┘
```

### Sections

| Section | Purpose | Primary actions |
|---|---|---|
| Home | Triage board: what needs attention now | open problem, mark seen, refresh |
| Live | Stream recent/new events and drill into detail | follow/pause, filter, detail, mark seen |
| Health | Doctor summary and remediation hints | refresh, copy fix command, open details |
| Memory | Review candidate memories | accept, reject, skip, edit/distill, evidence |
| Sessions | Active/stale/recent sessions and handoff | open session, handoff, close stale sessions after confirmation |

`Sessions` can start as a placeholder if v0.18 scope needs to stay small, but the shell should reserve the section so the cockpit direction is clear.

## Global keyboard model

| Key | Global meaning | Notes |
|---|---|---|
| `?` | Contextual help | Always available; first line shows global keys, second section shows screen actions |
| `:` | Action palette / command mode | Lists available cockpit actions, not arbitrary shell commands |
| `/` | Filter/search current list | No-op screens must explain why filtering is unavailable |
| `Tab` / `Shift-Tab` | Next / previous section or focus region | Mirrors lazygit-style transfer |
| `1`-`5` | Jump to section | Visible in header |
| `Esc` | Back/cancel/close overlay | Never quits from a nested screen; from Home it clears overlay/filter first, then no-op |
| `q` / `Ctrl-C` | Quit cockpit | The only quit keys |
| `r` | Refresh current screen | Shows loading and last refreshed timestamp |
| `Enter` | Open selected item / confirm in dialog | Never destructive without a confirmation dialog |
| `j/k`, `↑/↓` | Move selection | Shared with current TUI surfaces |
| `g/G`, `PgUp/PgDn` | Jump/scroll | Shared with current TUI surfaces |

### Screen-local keys

Local keys are allowed only when they are also shown in the footer/help/action palette. Examples:

- Live: `f` follow/pause, `m` mark seen
- Health: `c` copy fix command, `x` execute fix is out of scope for now unless confirmation UX exists
- Memory: `a` accept, `r` reject should be avoided because global `r` refresh already exists; use an action palette or explicit labels before choosing final memory shortcuts
- Sessions: `H` handoff, `g` GC is not acceptable because `g` already means top; use palette + confirmation

## Home screen design

Home should show a prioritized queue of actionable cards.

```text
HOME · 3 items need attention

Problems
  [FAIL] doctor failures=1             Enter: Health · fix: traceary doctor --fix --dry-run
  [WARN] stale sessions=4              Enter: Sessions · action: close stale after preview

New activity
  [NEW] 12 events since last seen       Enter: Live · m mark seen
  [NEW] 2 memory candidates             Enter: Memory · review now

All clear
  hidden unless there are no Problems/New activity cards
```

### Priority order

1. Doctor failures
2. Hook/MCP failures
3. Stale active sessions
4. New candidate memories, remember-intent first
5. New events / recent failures
6. Large payload warnings
7. Informational healthy status

## Live screen design

Live should behave like a focused tail viewer, not a static list.

Required elements:

- follow/pause state visible in the header
- filter query visible when active
- selected row count, total row count, and truncation indicator
- detail opens as a pane/modal with a breadcrumb back to Live
- mark-seen action visible when there are new events

Empty state:

```text
No recent events.
Actions: r refresh · f follow · / filter · h Home
```

## Health screen design

Health should summarize doctor status first, then group checks by severity.

Required elements:

- summary chips: `fail`, `warn`, `pass`
- failure/warning checks above passes
- each check shows name, short message, and remediation command if available
- action palette includes copy command; execution remains out of scope until confirmation and safety rules are designed

## Memory screen design

The memory screen should not simply embed the standalone `memory inbox review` view unchanged. It should share cockpit shell and help while reusing the review model for the decision flow.

Required elements:

- inbox queue position (`2/7`), source (`remember-intent`, `extracted`, `hidden`), confidence/quality marker if available
- evidence and edit modes as overlays with clear Esc behavior
- explicit finish/apply step that summarizes accepted/rejected/distilled/skipped counts
- empty state that explains “no candidate memories” and points to accepted memory search/list commands only as secondary fallback

## Sessions screen design

Sessions is the missing cockpit surface implied by `top` and `handoff`.

Initial v0.18 option:

- Show active sessions and stale sessions using existing top snapshot data.
- Enter opens session detail/handoff preview.
- Stale cleanup is shown as a guided action but requires confirmation and should default to dry-run preview.

If implementation scope is tight, ship Sessions as a read-only section first and defer cleanup actions.

## Layout behavior

| Terminal size | Behavior |
|---|---|
| `<80 cols` | Single-column content; keep header/footer short; hide secondary metadata behind help/detail |
| `80-119 cols` | Single-column cards/lists with compact metadata |
| `>=120 cols` | Two-pane layout where useful: list left, detail/summary right |

The layout must not rely only on color. Selection should use a prefix, border, or glyph in addition to color.

## State and feedback rules

- Loading state must appear within 100 ms of action dispatch.
- Refresh updates `loaded=` or `refreshed=` timestamp.
- Errors stay in the current screen and expose retry/back actions.
- Mark-seen changes counts immediately after successful state write.
- Follow mode must be visibly paused when the user scrolls away from the tail end, or the UI must make it clear that selection and follow are independent.
- No screen should render more rows than can be navigated predictably; use viewport/windowing for long lists.

## Non-goals for v0.18

- Mouse support
- Running arbitrary shell commands from inside the cockpit
- Auto-remediating doctor warnings without explicit confirmation
- Replacing direct CLI subcommands
- Full theming/configurable keybindings

## Implementation waves

1. **Design/audit (#1030):** land this design baseline and dogfood notes.
2. **Shell (#1031):** persistent header/footer, sections, global key semantics, Esc/back behavior.
3. **Discoverability (#1032):** contextual help and action palette.
4. **Home triage (#1033):** actionable cards and zero states.
5. **Dogfood/regression (#1034):** scripted task scenarios and layout snapshots.

## Acceptance checklist for v0.18

- A new user can answer “what should I do next?” from Home without reading docs.
- `?` explains the current screen, not only fallback CLI commands.
- `Esc` never unexpectedly exits from nested screens.
- Every visible warning has a visible action target.
- Live tail can be paused, filtered, refreshed, and drilled into from one screen.
- Memory review is reachable and finish/apply semantics are clear.
- Doctor warnings are grouped by severity with actionable remediation hints.
- Narrow terminal snapshots remain usable.
