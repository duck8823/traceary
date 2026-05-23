# Cockpit UI/UX design baseline (v0.18)

[English](./cockpit-ui-ux-design.md)

この文書は #1030 と v0.18 cockpit usability overhaul の設計ベースラインです。実装より先に固定します。v0.17 の cockpit は `top`、`tail`、`doctor`、`memory inbox review` という必要な面を集めましたが、日常利用に耐える interaction model にはまだ届いていません。

## ユーザーフィードバック

`traceary tui` は使いにくい。問題は underlying data の価値ではなく、TUI が自分自身の操作方法を説明できておらず、navigation が安定せず、mode ごとの隠れた key を覚えさせている点です。

## 設計原則

1. **コマンド一覧ではなく cockpit にする。** TUI 内では `top`、`tail`、`doctor`、`memory inbox review` を覚えなくてもよい operator console にする。
2. **常に現在地が分かる。** すべての画面で「どこにいるか」「何が選択されているか」「何が変わったか」「次に何ができるか」を分かるようにする。
3. **shortcut は覚える前に見つけられる。** single-key shortcut は残すが、contextual action list と現在画面向けの `?` help を必ず用意する。
4. **Home は triage board。** count を並べるだけではなく、今 attention が必要なものを優先する。
5. **CLI escape hatch は残す。** direct subcommand は script、専用 terminal pane、power user のために安定させる。
6. **破壊的な魔法はしない。** memory accept/reject や session GC のようなデータ変更は明示的にし、可能な限り確認・取り消し可能性を持たせる。

## 参考にするパターン

これは reference であり、見た目を clone するという意味ではありません。

| Product / guide | 採用するパターン | 避けるパターン |
|---|---|---|
| lazygit | persistent panel、常時 `?`、context-specific key hint、`Tab`/number navigation、`/` filter | Git 固有の mental model、初期画面から shortcut が多すぎる密度 |
| k9s | command/filter mode、`?` help、素早い view switch、予測しやすい Esc/cancel | Kubernetes 固有の resource grammar、破壊的な one-key action |
| btop / htop | glanceable な status region、安定した screen zone、明確な selected row | next action のない telemetry wall |
| Apple HIG keyboard guidance | 一般的な keyboard 期待値を尊重し、驚きの少ない shortcut と keyboard-only 操作にする | 画面ごとに共通 key の意味を変える |
| Material accessibility guidance | focus、state、feedback を明確にし、navigation/menu label は action を説明する | focus が隠れる・状態変化が曖昧 |
| Bubble Tea | model/update/view を明示し、小さい state machine を合成する | 1 つの巨大 model に screen-specific behavior が漏れる構造 |

参考リンク:

- lazygit keybindings: https://github.com/jesseduffield/lazygit/blob/master/docs/keybindings/Keybindings_en.md
- lazygit configuration/keybinding reference: https://github.com/jesseduffield/lazygit/blob/master/docs/Config.md
- k9s commands/key bindings: https://k9scli.io/topics/commands/
- btop interactive tutorial/key bindings: https://btop.one/
- Apple keyboard HIG: https://developer.apple.com/design/human-interface-guidelines/keyboards/
- Material accessibility: https://m2.material.io/design/usability/accessibility.html
- Bubble Tea: https://github.com/charmbracelet/bubbletea

## 現状 cockpit audit

確認対象: `presentation/cli/cockpit.go` と `presentation/cli/tui/` の shared TUI primitives。

### できていること

- 明示 entrypoint (`traceary tui`) は正しい。bare `traceary` は CLI help のままでよい。
- 最初の surface はある: home、live tail、doctor、event detail、memory review。
- local last-seen state を使って new memory / event count を出せる。
- 既存の direct command は script や fallback として残っている。

### 主な usability failure

| Failure | Current behavior | 問題 |
|---|---|---|
| navigation model が隠れている | Home は `d`、`t`/`l`、`m`; 他画面は `h`; memory review は独自 nested keys | operator が visible structure ではなく mode を記憶する必要がある |
| persistent shell がない | 各 mode が独自 title/footer を描画する | 現在地と global action が画面間で安定しない |
| Home が count dump | `ATTENTION`、`OVERVIEW`、`Cockpit surfaces` が text section | count が next action や priority に結びつきにくい |
| Help が fallback command 寄り | Home の `?` は CLI fallback command を表示する | TUI の操作モデルを学べない |
| Esc semantics が衝突 | shared keymap では `esc` が quit | lazygit/k9s 風の back/cancel 期待と違い、誤終了リスクがある |
| live tail に filter/search affordance がない | shared keymap に `/` はあるが cockpit live では使っていない | noisy な event stream を TUI 内で絞れない |
| doctor pane が read-only text | fix command は出るが action menu/copy/run affordance がない | cockpit が remediation を手伝えるか分からない |
| detail screen で origin context が薄い | detail は home/live に戻れるが breadcrumb が弱い | list/detail の関係を見失いやすい |

## 目標 information architecture

Cockpit は persistent global chrome を持つ tabbed operator console にする。

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

| Section | 目的 | Primary actions |
|---|---|---|
| Home | 今 attention が必要なものの triage board | open problem, mark seen, refresh |
| Live | recent/new event stream と detail drill-down | follow/pause, filter, detail, mark seen |
| Health | doctor summary と remediation hints | refresh, copy fix command, open details |
| Memory | candidate memory review | accept, reject, skip, edit/distill, evidence |
| Sessions | active/stale/recent sessions と handoff | open session, handoff, close stale sessions after confirmation |

`Sessions` は v0.18 scope を小さくするなら placeholder から始めてもよい。ただし cockpit の方向性を明確にするため、shell 上の section として予約する。

## Global keyboard model

| Key | Global meaning | Notes |
|---|---|---|
| `?` | contextual help | 常に利用可能。global keys と screen actions を分けて表示する |
| `:` | action palette / command mode | arbitrary shell command ではなく cockpit action を一覧する |
| `/` | current list の filter/search | filter できない画面では理由を説明する |
| `Tab` / `Shift-Tab` | 次/前の section または focus region | lazygit 風の transfer を狙う |
| `1`-`5` | section へ jump | header に常時表示する |
| `Esc` | back/cancel/overlay close | nested screen からは quit しない。Home では overlay/filter clear 後 no-op |
| `q` / `Ctrl-C` | cockpit を終了 | quit key はこの 2 つだけ |
| `r` | current screen refresh | loading と last refreshed timestamp を表示する |
| `Enter` | selected item open / dialog confirm | 確認 dialog なしに破壊的 action をしない |
| `j/k`, `↑/↓` | selection 移動 | 現行 TUI surface と共有 |
| `g/G`, `PgUp/PgDn` | jump/scroll | 現行 TUI surface と共有 |

### Screen-local keys

Local key は footer/help/action palette に表示される場合だけ許可する。例:

- Live: `f` follow/pause、`m` mark seen
- Health: `c` copy fix command。`x` execute fix は confirmation UX ができるまで scope 外
- Memory: `a` accept はよいが、`r` reject は global `r` refresh と衝突するため避ける。最終 shortcut は action palette や明示 label を先に設計する
- Sessions: `H` handoff は可。`g` GC は `g` top と衝突するため不可。palette + confirmation にする

## Home screen design

Home は actionable card の priority queue にする。

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

Live は static list ではなく focused tail viewer にする。

必須要素:

- follow/pause state が header で分かる
- filter query が active 時に見える
- selected row count、total row count、truncation indicator
- detail は breadcrumb 付き pane/modal として Live に戻れる
- new events がある場合は mark-seen action が見える

Empty state:

```text
No recent events.
Actions: r refresh · f follow · / filter · h Home
```

## Health screen design

Health は doctor status summary を先に出し、その後 severity ごとに check を group する。

必須要素:

- `fail`、`warn`、`pass` の summary chip
- failure/warning check を pass より上に出す
- 各 check は name、short message、remediation command を表示する
- action palette には copy command を入れる。execution は confirmation/safety rule が設計されるまで scope 外

## Memory screen design

Memory screen は standalone `memory inbox review` view をそのまま埋め込むだけでは不十分。decision flow は review model を再利用しつつ、cockpit shell と help を共有する。

必須要素:

- inbox queue position (`2/7`)、source (`remember-intent`, `extracted`, `hidden`)、可能なら confidence/quality marker
- evidence / edit mode は overlay として扱い、Esc behavior を明確にする
- finish/apply step で accepted/rejected/distilled/skipped counts をまとめる
- empty state は “candidate memories なし” を説明し、accepted memory search/list command は secondary fallback としてだけ示す

## Sessions screen design

Sessions は `top` と `handoff` から implied されている missing cockpit surface。

初期 v0.18 option:

- existing top snapshot data を使って active sessions と stale sessions を表示する
- Enter で session detail / handoff preview を開く
- stale cleanup は guided action として表示するが、confirmation 必須、default は dry-run preview

scope が厳しい場合、Sessions はまず read-only section として出し、cleanup action は defer する。

## Layout behavior

| Terminal size | Behavior |
|---|---|
| `<80 cols` | single-column。header/footer は短くし、secondary metadata は help/detail 側へ逃がす |
| `80-119 cols` | compact metadata 付き single-column cards/lists |
| `>=120 cols` | 有効な場所だけ two-pane。list left、detail/summary right |

色だけに依存しない。selection は color に加えて prefix、border、glyph のいずれかで表現する。

## State and feedback rules

- action dispatch から 100 ms 以内に loading state を出す。
- refresh は `loaded=` または `refreshed=` timestamp を更新する。
- error は current screen に残し、retry/back action を表示する。
- mark-seen は state write 成功後に count を即時更新する。
- user が tail end から離れて scroll したとき follow mode を visible に pause する、または selection と follow が独立していることを UI で明示する。
- 長い list は viewport/windowing し、操作できない量の row をそのまま描画しない。

## v0.18 の非目標

- mouse support
- cockpit 内から arbitrary shell command を実行すること
- 明示確認なしの doctor auto-remediation
- direct CLI subcommand の置き換え
- full theming / configurable keybindings

## Implementation waves

1. **Design/audit (#1030):** この design baseline と dogfood note を land する。
2. **Shell (#1031):** persistent header/footer、sections、global key semantics、Esc/back behavior。
3. **Discoverability (#1032):** contextual help と action palette。
4. **Home triage (#1033):** actionable cards と zero states。
5. **Dogfood/regression (#1034):** scripted task scenarios と layout snapshots。

## v0.18 acceptance checklist

- 新規ユーザーが Home を見て「次に何をすべきか」を docs なしで判断できる。
- `?` は fallback CLI command ではなく current screen を説明する。
- `Esc` は nested screen から予期せず終了しない。
- visible warning には必ず visible action target がある。
- Live tail は 1 画面内で pause、filter、refresh、detail drill-down ができる。
- Memory review への導線と finish/apply semantics が明確。
- Doctor warnings は severity ごとにまとまり、remediation hints が actionable。
- narrow terminal snapshot でも使える。
