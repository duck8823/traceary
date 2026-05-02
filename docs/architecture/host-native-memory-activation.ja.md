# Host-native memory activation contract

[English](./host-native-memory-activation.md)

この ADR は、accepted な Traceary durable memory を Claude Code と Gemini CLI の native context surface へ activation するための v0.13.0 契約を定義します。

Status: implementation planning として採用。ただし Claude Opus review はローカル認証の問題で未完了です。

## 背景

Traceary は durable memory をローカル SQLite store に保持します。accepted memory が source of truth であり、host file は review 済み fact を coding agent から見えるようにする projection です。

v0.12.0 では Codex activation を安全な managed-file projection として実装しました。

- 明示的な `status`、`dry-run`、`diff`、`apply` mode
- 暗黙の filesystem 変更なし
- Traceary-managed marker
- managed region 外の user-authored content の保持
- newer marker version や malformed managed block の上書き拒否
- symlink 拒否と atomic write
- `traceary doctor` での可視化

v0.13.0 では、同じ安全モデルを Claude Code と Gemini CLI に拡張します。新しい論点は、Claude / Gemini が `CLAUDE.md` / `GEMINI.md` という host instruction file から context を読むことであり、これらの file は既に user-authored で、project によっては Git 管理されている点です。

## host documentation baseline

この契約は host-owned auto-memory store ではなく、host が support する markdown import に依存します。

- Claude Code は session 開始時に `CLAUDE.md` を読み、`CLAUDE.md` 内の `@path/to/import` を support します。import された file は launch 時に context へ展開され、relative path は import を含む file から解決されます。外部 import では初回 approval dialog が出る可能性があります。Source: <https://code.claude.com/docs/en/memory>
- Claude auto memory は host-owned で、`~/.claude/projects/<project>/memory/` に保存され、session 中に Claude 自身が read/write します。Traceary は既定ではここへ書きません。Source: <https://code.claude.com/docs/en/memory>
- Gemini CLI は階層的な `GEMINI.md` context file を読み、`GEMINI.md` 内の `@file.md` import と relative / absolute import path を support します。Source: <https://google-gemini.github.io/gemini-cli/docs/cli/gemini-md.html>
- Gemini `save_memory` は user の `~/.gemini/GEMINI.md` の `## Gemini Added Memories` 配下に fact を append します。Traceary はこの section を変更してはいけません。Source: <https://google-gemini.github.io/gemini-cli/docs/tools/memory.html>
- Gemini の Memory Import Processor は import depth、circular import handling、access validation を文書化しています。Source: <https://google-gemini.github.io/gemini-cli/docs/core/memport.html>

## 決定

Claude と Gemini では two-file activation contract を使います。

1. host context file 内の小さな Traceary-managed import stub
2. accepted memories を render した Traceary-managed external memory file

host context file は安定した integration point です。external memory file は頻繁に更新される projection です。import stub が既に正しい場合、memory refresh は external memory file だけを更新するべきです。

Codex は v0.13.0 では変更しません。activation target は引き続き `~/.codex/memories/traceary.md` のような Traceary-managed native memory file です。

## 既定 target

### Claude

`--target claude` を実装するときの契約は次です。

- default activation root: 検出した project root。project root が無い場合は current working directory
- default host context file: `<root>/CLAUDE.md`
- default external memory file: `<root>/.traceary/memories/claude.md`
- `CLAUDE.md` に render する default import line: `@./.traceary/memories/claude.md`

`CLAUDE.local.md` は将来の user-local mode 候補として残します。ただし v0.13.0 の既定にはしません。まず `status`、`doctor`、test が検証できる決定論的な path を 1 つ shipping します。

### Gemini

`--target gemini` を実装するときの契約は次です。

- default activation root: 検出した project root。project root が無い場合は current working directory
- default host context file: `<root>/GEMINI.md`
- default external memory file: `<root>/.traceary/memories/gemini.md`
- `GEMINI.md` に render する default import line: `@./.traceary/memories/gemini.md`

Traceary は Gemini の `## Gemini Added Memories` section を rewrite / reorder してはいけません。その section を含む file に Traceary-managed import stub を追加する場合も、managed region 外は byte-for-byte で保持します。

### override

既存の activation flag は、host ごとの解決をしつつ意味を保ちます。

- `--root <dir>` は activation root を解決します。Claude/Gemini では、この root に host context file と `.traceary/memories/<target>.md` external file が置かれます。
- `--path <file>` は host context file を明示指定します。external memory file は context file の directory から `.traceary/memories/<target>.md` として導出します。
- `--path` は `--root` より優先します。
- v0.13.0 では、実装上どうしても必要だと dogfood で分かるまで、2 つ目の path flag は増やしません。必要になった場合は `--memory-path` を優先し、実装前に文書化します。

import path は、両 file が同じ root を共有する限り host context file からの relative path として render します。absolute import path は、将来の明示 override で relative rendering が不可能な場合だけ許可します。

## managed region

import stub と external memory block は別々の marker 契約を持ちます。

host context file stub:

```md
<!-- traceary-memory-import:begin:v1 -->
<!-- DO NOT EDIT: this import is managed by Traceary. Run `traceary memory activate --target <host> --dry-run --diff` before applying updates. -->
@./.traceary/memories/<host>.md
<!-- traceary-memory-import:end -->
```

external memory file:

```md
<!-- traceary-memories:begin:v1 -->
<!-- DO NOT EDIT: this block is managed by Traceary. Hand edits will be overwritten by `traceary memory export` or `traceary memory activate`. -->

# Traceary-managed <host> memories

...
<!-- traceary-memories:end -->
```

external memory block は既存の `memory export` renderer を再利用し、Codex / Claude / Gemini の projection を一貫させます。

## status semantics

Claude/Gemini の status は 1 file ではなく file pair から計算します。

| State | Meaning |
| --- | --- |
| `missing` | host context file が無い、import stub が無い、external memory file が無い、または external file に Traceary-managed memory block が無い。malformed / unsafe condition は見つかっていない。 |
| `stale` | stub が古い expected path を指している、または external memory block が現在の accepted-memory projection と異なる。 |
| `in_sync` | host context file に supported な Traceary import stub が 1 つだけあり、expected external memory file を指していて、その external file の Traceary memory block が現在の accepted-memory projection と一致する。 |
| `invalid` | どちらかの file を安全に解釈・書き込みできない。unsupported symlink、directory target、duplicate marker、orphan marker、newer marker version、malformed managed region、unreadable file、明示 override なしで activation root 外へ逃げる import path など。 |

JSON output は host context file と external memory file の component-level detail を出し、`traceary doctor` が具体的な remediation を提示できるようにします。

## apply semantics

`--status` と `--dry-run` は read-only です。変更するのは `--apply` だけです。

apply は idempotent でなければならず、Traceary-managed region 外の user-authored content を保持します。Claude/Gemini では次の順で書きます。

1. external memory file を render して安全に書く
2. host context import stub が missing / stale の場合だけ host context file を書く
3. stub が既に in sync なら host context file は触らない

最初の write が成功し、2 つ目の write が失敗した場合、複雑な rollback はしません。次の `status` が残った missing/stale/invalid condition を報告し、再度 `apply` すれば安全に収束できる必要があります。

atomic write、permission preservation、parent directory creation、symlink refusal、newer-marker refusal は v0.12 Codex activation 実装に揃えます。

## tracked project file policy

Traceary が project file を更新してよいのは、明示的な `--apply` のときだけです。`doctor`、`status`、`dry-run` が `CLAUDE.md`、`GEMINI.md`、`.traceary/memories/<host>.md` を変更してはいけません。

host context file が Git 管理されている場合でも、明示的な `--apply` では managed stub を更新してよいです。ただし diff は review 可能で、managed region だけに限定されなければなりません。Traceary は stage / commit しません。

host context file が存在しない場合、`--apply` は managed stub だけを含む file を作成してよいです。dry-run output と doctor remediation command では、その file 作成予定を明示します。

## rejected alternatives

### Claude auto memory に直接書く

却下。Claude auto memory は `~/.claude/projects/<project>/memory/` 配下の host-owned store で、Claude 自身が read/write します。ここへ書くと Traceary の accepted-memory source of truth を迂回し、host-managed format に密結合します。

### Gemini `## Gemini Added Memories` に直接書く

却下。Gemini `save_memory` は user の global `GEMINI.md` にあるこの section を所有しています。Traceary は、review 済み accepted memories と Gemini memory tool が append した fact を混在させるべきではありません。

### `CLAUDE.md` / `GEMINI.md` に full memory block を直接注入する

default としては却下。単純ですが、accepted memory が変わるたびに user/project instruction file が churn します。import-stub strategy なら、host-native context loading を使いつつ、頻繁な更新を `.traceary/memories/<host>.md` に閉じ込められます。

### 手動 `memory export --out` だけにする

v0.13.0 では却下。export は引き続き有用ですが、activation には read-only status、dry-run/diff、明示 apply、doctor integration、idempotent remediation command が必要です。

### host context file から Traceary file へ symlink する

却下。symlink は platform 差が大きく、既存 activation safety model を弱めます。Traceary は通常の markdown import を render し、書き込み対象 symlink は拒否します。

## implementation sequence

1. #889: この contract / ADR。
2. #890: Codex behavior を変えずに、host-agnostic な activation target resolution、marker parsing、status、safe writer code を抽出する。
3. #891: two-file activation planning primitive と component-level status/diff output を追加する。
4. #892: Claude read-only `status`、`dry-run`、`diff` を wire する。
5. #893: Claude `apply` と doctor integration を wire する。
6. #894: Gemini read-only `status`、`dry-run`、`diff` を wire する。
7. #895: Gemini `apply` と doctor integration を wire する。
8. #896: temporary HOME/project fixture で Claude/Gemini workflow を document / dogfood する。
9. #897: v0.13.0 release metadata と changelog を準備する。

## review notes

Gemini review は 2 回実行しました。初回は import stub に反対しましたが、host が native に import を load しないという前提でした。公式の Claude / Gemini import docs を提示して再レビューしたところ、import-stub strategy は採用可能とされ、実リスクとして Claude の初回 external-import approval dialog、安全な stub injection、host context file からの相対 import-path resolution が挙げられました。

Claude Opus 4.7 Max review はローカルで試行しましたが、この環境の Claude Code が認証されていません。

```text
$ claude auth status
{"loggedIn": false, "authMethod": "none", "apiProvider": "firstParty"}

$ claude --print --model opus --effort max --permission-mode plan ...
Not logged in · Please run /login
```

この ADR 後の implementation PR は、Claude Opus review を取得するまで draft / blocked のままにします。例外にする場合は、maintainer が PR 上で明示的に受け入れる必要があります。
