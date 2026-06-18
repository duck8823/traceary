# 運用前提

[English](./README.md)

このガイドでは、Traceary のローカル SQLite store と generated hook script が実行時に置いている前提を明文化します。
何を保証しているのか、どこが best-effort なのか、どこで手動 override が必要になりうるのかを率直に説明します。

## SQLite の concurrency model

Traceary は、process 間調停を SQLite 自身に委ねています。

現在の前提:

- 通常利用は、同じ DB file を複数の短命な CLI / hook / MCP process が共有する形
- write は小さな append 系操作（`events`、`command_audits`、session 境界）が中心
- file level の write 直列化は SQLite が安全に扱う

Traceary が接続時に適用している設定:

- `journal_mode=WAL` — reader（例: `traceary tail` の poll）と writer が互いをブロックせず並行に処理できる
- `synchronous=NORMAL` — WAL モードで推奨される durability 設定。fsync は checkpoint 境界でのみ実行される
- `busy_timeout=5000` — 一時的なロック競合時は最大 5 秒 SQLite が自動で retry し、`SQLITE_BUSY` を即座に返さない
- `foreign_keys=1` — 外部キー制約を有効化

WAL モードは DB 本体の横に `<db>-wal` と `<db>-shm` の sidecar file を生成します。バックアップ/リストアはこれらを既に扱っていますが、手動で DB をコピーする場合は sidecar も一緒にコピーする必要があります。

現在 Traceary が**追加していない**もの:

- custom background writer や queue
- SQLite の `busy_timeout` を超える application-level retry loop
- distributed / multi-host coordination

実務上の見方:

- 1 台のマシンで数本の並列 AI session を回す用途はスコープ内
- 同じ DB path に対する極端に高い write volume は、現時点で tuning 対象ではない
- 5 秒の busy window を超えて `SQLITE_BUSY` が出る場合は、並列 writer を減らす、作業フローごとに DB path を分ける、で対処してください

## Hook session state の前提

generated hook script は、最後に解決した session ID を小さな local state file に保存します。

既定の state key は次で決まります。

- client 名
- 明示指定された `TRACEARY_HOOK_STATE_KEY`
- それが無ければ現在の `PPID`（必要なら `$$` に fallback）

つまり、現在の hook 設計は次を前提にしています。

- 1 つの interactive client session に属する hook invocation は、だいたい安定した parent process identity を共有する
- client が、`PPID` を grouping key として使っても大きく崩れない process tree から hook を呼ぶ

これは protocol level の保証ではなく、意図的に best-effort です。
client 側の process model が変わった場合や、wrapper script により parent/child 関係が変わる場合は、`TRACEARY_HOOK_STATE_KEY` で grouping key を明示してください。

## 既知の failure mode

### `doctor` は通るが hooks が発火しない

`traceary doctor` は、DB access、hook script の materialize、対象 config file に Traceary 管理下 entry があるか、までは確認します。
ただし、third-party client 自体を起動して、検証時と同じ形で全 hook event が発火することまでは保証しません。

### client によっては session end が best-effort

- Claude Code: documented integration では dedicated `SessionEnd` を使える
- Codex CLI: host のセッション終了信号がない — `Stop` は応答ごとの turn 境界 (#1170) であり、Codex session は MCP `manage_session` または stale GC (`traceary session gc`) でのみ終了する
- Gemini CLI: `SessionEnd` も best-effort 扱い

session end の精度が重要なら、明示的な end hook を持つ client integration を優先してください。

### Hook state が別 session に付いてしまう

多くの場合、既定の PPID ベース grouping がローカル client process topology に合っていません。
より安定した grouping key が必要なら、hook 環境で `TRACEARY_HOOK_STATE_KEY` を明示してください。

### Audit reliability の dogfood signal

`traceary doctor` は bounded な recent command-audit window に対して `audit-reliability` check を出します。ここでは duplicate command-audit candidate group と、保存された audit input 内の `cwd` evidence が event の workspace metadata と食い違う workspace-drift candidate を報告します。

これは自動 cleanup ではなく dogfood review の信号として扱ってください。check は count と sample event ID だけを表示し、command input/output body は出しません。process-review の指標に使う前に、sample row を `traceary show <event_id>` で確認し、本当に duplicate / drift なのか、正当な繰り返し作業なのかを切り分けてください。

### active ingestion 中に cleanup を強く走らせる

`traceary store gc` は古い row を削除したあとに `VACUUM` を実行します。
同じ DB に対して多くの session が書き込んでいる最中に、強めの cleanup を background maintenance のように常時回す前提ではありません。
先に backup を取り、比較的静かなタイミングで実行してください。

## support している範囲と、たまたま動く範囲

現在 support している範囲:

- 1 台のマシン
- 作業フロー / project group ごとに使う 1 つの local SQLite file
- write volume が常識的な範囲の複数 human / AI session

動く可能性はあるが積極的には tuning していない範囲:

- 1 つの DB に対する高頻度な大量並列 writer
- hook process topology を大きく変える client wrapper
- non-POSIX な hook 実行環境

## 推奨 mitigation

1. hook generation を疑う前に `traceary doctor` を使う
2. 複数の作業フローで DB を分けたい場合は `TRACEARY_DB_PATH` を明示する
3. PPID grouping が不安定なら `TRACEARY_HOOK_STATE_KEY` を明示する
4. risky cleanup や手動調査の前に `traceary store backup create` を実行する
5. best-effort session-end hook に依存する場合は、自分たちの team automation に client 固有注意点を明記する

## 関連文書

- hooks integration: [`../hooks/README.ja.md`](../hooks/README.ja.md)
- storage model: [`../storage/README.ja.md`](../storage/README.ja.md)
- backup guide: [`../backup/README.ja.md`](../backup/README.ja.md)
- 定期メンテナンスタスク: [`./scheduled-tasks.ja.md`](./scheduled-tasks.ja.md)
- Python 依存の縮小計画: [`./python-dependencies.ja.md`](./python-dependencies.ja.md)
- repository tooling の方針: [`./repo-tooling.ja.md`](./repo-tooling.ja.md)
- Memory コマンド体系の整理計画: [`./memory-command-surface.ja.md`](./memory-command-surface.ja.md)
