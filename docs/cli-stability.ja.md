# CLI 安定性と非推奨ポリシー

[English](./cli-stability.md)

このドキュメントは、Traceary の CLI サーフェスについて運用者・統合者向けに公開する契約です。
どのコマンドが「公開サーフェス（public）」で、どれが「admin / メンテナンス」、どれが「plumbing / hidden / deprecated」かを定義します。
v0.14 系と続く v1.0 でコミットする「非推奨通知の出し方」「1 マイナー分の互換ウィンドウ」「v0 と v1 の削除ポリシー」もここで定めます。

コマンド単位のフラグや挙動の詳細は [CLI リファレンス](./cli/README.ja.md) にまとめています。
本ページは意図的にポリシー層に絞ってあるので、外部ツールや AI integration、SKILL パックがリファレンスを毎回読まずに安定性を判断できるリンク先として使えます。

## 目的

- v1.0 を見据え、v0.14 のコマンドサーフェスを「どこが公開で、どこがメンテで、どこが deprecation シムか」を明示する。
- 日常用途のコマンド（公開サーフェス）はマイナーリリースを跨いでバイト一致で安定させる。
- admin / メンテナンス系コマンドは公開サーフェスを壊さない範囲でマイナー境界で進化させる。
- スクリプト・AI SKILL・古いドキュメントの例文に対し、削除前に必ず 1 マイナー以上の移行猶予を与える。

## 安定性ティア

Traceary の各サブコマンドは必ず以下のいずれか 1 ティアに属します。
ティアにより「変更しても良い範囲」「変更してよい時期」「外部 caller への通知方法」が決まります。

| ティア | 表示 | 安定範囲 | リリースごとに許される変更 |
|---|---|---|---|
| **公開（public）** | `traceary --help` と `docs/cli/README.md` に掲載。 | コマンドパス、フラグ名、終了コード、stdout のテキスト形状、`--json` / `--id-only` / NDJSON のバイト形状、エラーメッセージ構造。 | マイナー間は **追加のみ**（新規フラグ、新規 JSON optional フィールド、新規サブコマンド）。破壊的変更は後述の deprecation フローを通り、最低 1 マイナーの重複期間を取る。 |
| **admin / メンテナンス** | `--help` の名前空間（`store`、`memory admin` など）と `docs/cli/README.md` に掲載。 | 文書化されたコマンドパス・フラグ集合、`--json` / `--dry-run` / `--apply` のセマンティクス（該当する場合）。 | マイナー間は追加可。破壊的変更は公開と同じ deprecation フローだが、運用者だけが触る範囲では「N で stderr 通知 → N+1 で削除」と速く進めてよい。 |
| **plumbing / hidden / deprecated** | `--help` から非表示 (`Hidden: true`)。CLI リファレンスでは「deprecated alias」または「cleanup-only」と明記。 | 置き換え先 canonical の引数・フラグ形状、stderr 非推奨通知のフォーマット。 | 通知中に名指しした次マイナーで削除可。新しい plumbing コマンドは進行中の移行をブリッジするためでない限り追加しない。 |

### 公開コマンド (v0.14)

公開サーフェスは運用者の日常用途サーフェスです。コマンドパス・フラグ名・stdout テキスト形状・`--json` / `--id-only` / NDJSON のバイト形状をマイナーリリースを跨いで安定に保ちます。

v0.14 の公開コマンド（用途別）：

- **イベント記録** — `traceary log`、`traceary audit`
- **読み取り / 観察** — `traceary list`、`traceary search`、`traceary tail`、`traceary timeline`、`traceary show`、`traceary context`、`traceary top`（および `traceary top --snapshot` / `--snapshot --json`）
- **セッション** — `traceary session start`、`traceary session end`、`traceary session handoff`（`--compact-only` を含む）、`traceary session list`、`traceary session tree`、`traceary session lineage`、`traceary session label`、`traceary session latest`、`traceary session active`
- **durable memory 日常 read** — `traceary memory list`、`traceary memory search`、`traceary memory show`
- **durable memory inbox** — `traceary memory inbox list`、`traceary memory inbox accept`、`traceary memory inbox reject`、`traceary memory inbox review`（TTY のみ）
- **durable memory store** — `traceary memory store remember`、`traceary memory store propose`、`traceary memory store distill`
- **hooks** — `traceary hooks print`、`traceary hooks install`、`traceary hooks guide`、`traceary completion`
- **MCP server** — `traceary mcp-server`
- **診断** — `traceary doctor`（alias `traceary status`）
- **replay / archive** — `traceary replay`
- **bundle import / export** — `traceary bundle export`、`traceary bundle import`

`traceary doctor` の JSON envelope（`sections` / `summary` / `exit_code` / 各 check のフィールド）、`traceary top --snapshot --json` の envelope（`sessions` / `failures` / `recent_commands` / `candidates`）、`traceary timeline --json`（`workspace_breakdown`）、`traceary session tree --json` の lineage フィールド、`traceary session handoff` の構造化テキストのフィールドラベルはいずれも公開契約の一部です。これらは `presentation/cli/testdata/` で golden test により固定されています。詳細は [JSON / snapshot contract test](./operations/json-contract-tests.ja.md) を参照してください。

> TTY 必須の公開コマンド（現状は `traceary memory inbox review`）は TTY 要件を明示し、stdin/stdout が TTY でないときは非ゼロ終了コードでスクリプト用フォールバックを案内します。新しい TTY-only 公開コマンドを追加するときも、必ず batch / scripted フォールバック経路を文書化してください。

### admin / メンテナンスコマンド (v0.14)

admin コマンドは運用者向けのメンテサーフェスです。`--help` には引き続き掲載され CLI リファレンスでも文書化されますが、日常 read 経路ではありません。対象が運用者だけのときは公開コマンドより速いペースで進化してよいですが、後述の非推奨通知ルールには従います。

v0.14 の admin コマンド：

- **ストア管理** — `traceary store init`、`traceary store backup create`、`traceary store backup restore`、`traceary store gc`
- **セッション管理** — `traceary session gc`（stale なセッションを終了する。`session` 名前空間配下に公開セッションサブコマンドと同じ位置で登録されているが、扱いとしては admin ティアのメンテナンス入口）
- **durable memory admin** — `traceary memory admin extract`、`traceary memory admin import codex`、`traceary memory admin import instructions`、`traceary memory admin export`、`traceary memory admin activate`、`traceary memory admin hygiene scan`、`traceary memory admin hygiene apply`、`traceary memory admin graph add`、`traceary memory admin graph list`、`traceary memory admin supersede`、`traceary memory admin expire`、`traceary memory admin set-validity`

### plumbing / hidden / deprecated コマンド (v0.14)

これらは `traceary --help` から非表示です。canonical 置き換えへ再ルーティングする deprecation シム、廃止サーフェスから移行する利用者向けの cleanup-only パス、または同梱の Traceary hook スクリプトから呼び出されるランタイム入口です。

v0.14 の hidden な deprecated alias（`Hidden: true` で登録、動作は維持、stderr に非推奨通知、v0.15 で削除予定）：

- `traceary memory accept`、`traceary memory reject`、`traceary memory remember`、`traceary memory propose`、`traceary memory distill`、`traceary memory extract`、`traceary memory supersede`、`traceary memory expire`、`traceary memory set-validity`、`traceary memory import codex`、`traceary memory import instructions`、`traceary memory export`、`traceary memory activate`、`traceary memory hygiene scan`、`traceary memory hygiene apply`、`traceary memory graph add`、`traceary memory graph list` — 完全なマッピングは [memory コマンド体系の整理計画](./operations/memory-command-surface.ja.md) を参照。

同梱の Traceary hook スクリプトから呼び出される hidden ランタイム入口（`Hidden: true` で登録、stderr 非推奨通知は出さない）：

- `traceary hook session`、`traceary hook audit`、`traceary hook compact`、`traceary hook subagent-start`、`traceary hook subagent-stop`、`traceary hook prompt`、`traceary hook transcript` — `traceary hooks print` / `traceary hooks install` が出力する hook スクリプトから呼び出される。
- `traceary hooks helper json-get`、`traceary hooks helper build-failure-output`、`traceary hooks helper normalize-git-remote` — 同じ同梱 hook スクリプトが使う内部ヘルパー。

これら runtime 入口の安定性 / 非推奨ポリシー：

- これらは Traceary バイナリと、そのバイナリが生成する hook 設定との間の内部契約として扱う。運用者や外部スクリプトが直接呼び出す対象ではなく、canonical な運用者入口は `traceary hooks print` / `traceary hooks install` で、再インストールするとインストール済みバージョンに合った hook 設定が再生成される。
- コマンドパスと引数形状は patch リリース (`v0.N.x`) では安定。
- マイナー境界 (`v0.N.0` → `v0.(N+1).0`)、および v1.0 以降の `v1.x` マイナー間でも、新マイナーの `traceary hooks install` が互換 script を再生成し、CHANGELOG で「hook を再インストールする必要がある」旨を案内することを前提に、改名・削除・引数形状変更を公開の stderr 非推奨フローを通さずに行ってよい。
- 新しい hidden ランタイム入口の追加も同じルール。マイナー境界で追加してよく、同じバージョンの `traceary hooks print` / `traceary hooks install` 更新と組で出荷する。

旧サーフェスから移行中の利用者向けに残している hidden な cleanup-only コマンド（置き換え先なし、v0.15 で削除予定）：

- `traceary integration codex uninstall` — 廃止された install path から移行する利用者のために、Traceary 管理下の Codex プラグイン状態を片付ける専用パス。`traceary integration codex install` 自体は v0.14.0 で廃止済みで、旧名を実行すると Codex 公式 `/plugins` への案内 usage error で終了します。

すでに非機能の usage-error スタブとなっている top-level alias（既に動作していないため削除予定の日付はありません。あくまで案内目的のスタブ）：

- `traceary init` → `traceary store init`
- `traceary backup` → `traceary store backup ...`
- `traceary gc` → `traceary store gc`
- `traceary handoff` → `traceary session handoff`
- `traceary compact-summary` → `traceary session handoff --compact-only`

## 非推奨通知の出し方

公開・admin の command path、フラグ、JSON フィールド名、出力形状を caller に影響する形で変更する必要が生じたとき、Traceary は以下の単一フローに従います。

### stderr 通知

非推奨コマンドは実行のたびに stderr に **必ず 1 行** 以下の形式を出します。

```
DEPRECATED: this command is deprecated, use `<canonical replacement>` instead. Removal target: v<X.Y>.
```

`TRACEARY_LANG=ja` のときは同じ構造の日本語版が出ます。

```
DEPRECATED: このコマンドは非推奨です。代わりに `<canonical replacement>` を使用してください。削除予定: v<X.Y>。
```

通知ルール：

- 通知文には canonical 置き換え先のコマンド（サブコマンドのフルパス、たとえば `traceary memory admin hygiene scan`）を含める。親グループ名だけで省略しない。
- 通知文に削除予定バージョン（`v0.15`、`v1.0` など）を含める。
- 通知は **stderr** に出す。これにより stdout / `--json` / NDJSON の出力は canonical コマンドとバイト一致を保てる。Cobra 組み込みの `Deprecated` フィールドは stdout に出すため、Traceary は自前で stderr に書く。
- 1 回の実行で通知は 1 行のみ。非推奨コマンドが親グループでサブコマンドが実エントリーの場合も、実行された leaf に対して 1 度だけ発火し、canonical leaf の正確なパスを指す。

### stdout / JSON / NDJSON 互換性

非推奨ウィンドウの間、非推奨コマンドは以下を維持しなければなりません。

- 旧来と同じ stdout テキストバイト
- 旧来と同じ `--json` 出力（フィールド名、契約に明記された並び、NDJSON の 1 行形状）
- 旧来と同じ終了コード
- 旧来と同じ `--id-only` バイト形状

非推奨 alias に新しい optional フラグを足してよいのは、canonical 置き換え先にも同じフラグがある場合のみ（caller が引数を書き換えずに移行できる範囲）。

コマンドではなくフラグを非推奨にする場合も、同じ形式の stderr 通知を使います。フラグは非推奨ウィンドウの間は旧来挙動を維持し、通知文には置き換え先フラグを含め、`CHANGELOG.md` の "Deprecated" にエントリを追加します。

### ドキュメント要件

すべての非推奨化は同じ変更で 3 箇所を更新します。

1. CLI リファレンス（`docs/cli/README.md` と `docs/cli/README.ja.md`）— 非推奨パスに置き換え先と削除予定を明記。
2. CHANGELOG（`CHANGELOG.md` と `CHANGELOG.ja.md`）— "Deprecated" または "Changed" の項に、パス・置き換え先・削除予定バージョンを書く。
3. 大きなサーフェス整理の一部のときは関連する operations / 計画ドキュメント（例: memory tree 再編なら [memory コマンド体系の整理計画](./operations/memory-command-surface.ja.md)）。

## 互換性ウィンドウ

### 1 マイナー互換ウィンドウ（既定）

非推奨ウィンドウの既定は **1 マイナー** です。v0.N.0 で非推奨にしたコマンド・フラグ・JSON 形状は、v0.N.x の patch を通して通知付きで動作し続け、v0.(N+1).0 で削除されます。

この既定に従う例：

- v0.14.0 で導入した memory tree のグループ化（`memory inbox` / `memory store` / `memory admin`）。フラットな verb（`memory remember`、`memory propose`、`memory accept` など）は v0.14.x 全体で hidden な deprecated alias として動作し、v0.15.0 で削除します。詳細は [memory コマンド体系の整理計画](./operations/memory-command-surface.ja.md)。
- 廃止された Codex install ヘルパー。v0.14.0 では片付け用の uninstall を hidden cleanup-only として残し、v0.15.0 で削除します。

### 出力に影響する破壊的変更は窓を延ばすことがある

公開 `--json` envelope、構造化テキスト契約（`traceary session handoff` など）、AI SKILL が直接ワイヤしている公開コマンドパスといった「重く scripted されている」サーフェスの破壊的変更については、メンテナの裁量で 1 マイナーより長い窓を取ることがあります。決定は元イシューと CHANGELOG エントリに記録します。これは例外的な扱いで既定ではありません。

### 非推奨ウィンドウが不要なケース

純粋に追加だけの変更には非推奨ウィンドウは不要です。

- 新しい公開サブコマンドの追加
- 新しい optional フラグの追加
- JSON オブジェクトの末尾に新しい optional フィールドを追加（consumer は未知フィールドを許容すること）
- `traceary doctor` の新セクション、`traceary top --snapshot` の新ペインの追加

これらを **削除・改名** すると破壊的変更になり、deprecation フローを通します。

## v0 と v1 での削除ポリシー

### v0.x 系列

Traceary は現状 `v0.x` 系列です。v0.x の意図は「v1.0 までに、予告された決まったリズムでサーフェスを安定化させる」ことです。

- **公開コマンド**: 破壊的変更はマイナー境界 (`v0.N.0` → `v0.(N+1).0`) で許容、上記の 1 マイナーウィンドウを使う。patch (`v0.N.x`) は非破壊。
- **admin コマンド**: 既定は公開と同じ。対象が運用者だけのときは「v0.N で非推奨 → v0.(N+1) で削除」のより速いペースをメンテナが選択してよい。
- **plumbing / hidden / deprecated コマンド**: 通知文に名指しされたマイナーで削除する。

v0.14.0 で除去された旧 top-level alias（`traceary init`、`traceary backup`、`traceary gc`、`traceary handoff`、`traceary compact-summary`）はこのモデルに従いました。v0.9.0 で非推奨、v0.14.0 で削除、その間は通知と置き換え先案内を継続出力。

### v1.0 以降

v1.0 リリース以降：

- **公開コマンド**: `v1.x` 系列全体で安定。破壊的変更はメジャー境界 (`v1.x` → `v2.0`) のみ。マイナー (`v1.0.0` → `v1.1.0`) は後方互換を保ち、既存の公開コマンドパス・フラグ名・終了コード・stdout 形状・文書化された JSON フィールド名は次マイナーでもバイト一致で動く。
- **admin コマンド**: `v1.x` 内のマイナー間でも後方互換だが、admin 専用フラグの追加・改名はマイナー境界で許容（上記 deprecation フローを通せば、最低 1 マイナーは stderr 通知付きで動く）。
- **plumbing / hidden / deprecated コマンド**: v0.x と同じく、通知に名指しされたマイナーで削除。
- **メジャー移行**: 将来 `v2.0` を計画するとき、`v1.x` 系列の最後のマイナー (`v1.last`) で `v2.0` で変える項目すべてに stderr 通知を出す。`v2.0` リリースノートには同じ集合を再掲し、外部 caller が見るべき移行リストが 1 箇所に揃う形にする。

要約: v0.x はマイナー境界で 1 マイナー overlap を取りながらサーフェスを動かす。v1.x は公開サーフェスをメジャー全体で凍結する。v2.0（あるとすれば）が次の公開サーフェス更新タイミング。

## 本ポリシーの対象外

このポリシーは CLI サーフェスを対象とします。以下は別ドキュメントで扱います。

- MCP tool registry の安定性 — [JSON / snapshot contract test](./operations/json-contract-tests.ja.md) と `presentation/mcpserver/testdata/` の registry snapshot を参照。
- hook capture の安定性 — [hook contract](./hooks/contract.ja.md) と [host coverage matrix](./hooks/host-coverage.ja.md)。
- ストレージ / SQLite スキーママイグレーション — [ストレージモデル](./storage/README.ja.md)。
- host-native memory activation marker の互換性 — [host-native memory activation contract](./architecture/host-native-memory-activation.ja.md)。

## 関連ドキュメント

- [CLI リファレンス](./cli/README.ja.md)
- [memory コマンド体系の整理計画](./operations/memory-command-surface.ja.md)
- [JSON / snapshot contract test](./operations/json-contract-tests.ja.md)
- [リリースガイド](./release/README.ja.md)
- [README](../README.ja.md)
