# Memory コマンド体系の整理計画 (v0.14)

[English](./memory-command-surface.md)

`traceary memory ...` は現在 18 個の直下サブコマンドを持ち、レビュー / 書き込み / ライフサイクル / hygiene / graph / host activation / import・export と用途がフラットに混在しています。一覧から目的別に追いづらく、admin 専用のコマンドが日常用途のコマンドと並んで表示されています。

この文書は v0.14 で進める「memory コマンド面のスリム化」の出発点となるベースラインです。本イシュー (#921) ではドキュメントのみを追加し、ランタイムの挙動は変更しません。

## 目的

- `memory` コマンドを「日常 read」「inbox レビュー」「durable 書き込み」「admin / host 連携」の 4 グループに整理する。
- 日常 read 用の `memory search` / `memory show` を変更しない（既存スクリプト・skill が壊れないようにする）。
- admin / host 連携・durable ライフサイクルは新しい namespace (`memory inbox` / `memory store` / `memory admin`) の下にまとめる。
- 既存パスはすべて v0.14 では hidden な deprecated alias として残し、v0.15 で削除する。1 リリース分の重複期間を確保する。

## 現在のコマンド (v0.13.1)

`traceary memory --help` の出力を元にしています。

| 現在のパス | 役割 | 備考 |
|---|---|---|
| `memory search` | 検索 | 日常 read |
| `memory show` | 詳細表示 | 日常 read |
| `memory list` | 一覧 | 日常 read |
| `memory inbox list` | candidate 一覧 | 既に nested |
| `memory inbox accept` | candidate を accept | 既に nested |
| `memory inbox reject` | candidate を reject | 既に nested |
| `memory accept` | candidate を accept (top-level 形式) | `memory inbox accept` と重複 |
| `memory reject` | candidate を reject (top-level 形式) | `memory inbox reject` と重複 |
| `memory propose` | candidate を記録 | inbox 系 |
| `memory distill` | candidate から accepted を作る | inbox 系 |
| `memory extract` | session から candidate を抽出 | inbox feeder |
| `memory remember` | accepted memory を記録 | durable 書き込み |
| `memory supersede` | accepted を置き換える | durable 書き込み |
| `memory expire` | active memory を expire | durable ライフサイクル |
| `memory set-validity` | validity window を設定 | durable ライフサイクル |
| `memory import codex` | `~/.codex/memories/*.md` を candidate として取り込む | admin / host 連携 (`memory import` 配下の実行 leaf) |
| `memory import instructions` | host instruction file の bullet を candidate として取り込む | admin / host 連携 (`memory import` 配下の実行 leaf) |
| `memory export` | accepted を host instruction file に書き出す | admin / host 連携 |
| `memory activate` | host-native activation の plan / apply | admin / host 連携 |
| `memory hygiene scan` | hygiene 問題を検出 | admin |
| `memory hygiene apply` | hygiene の修正を適用 | admin |
| `memory graph add` | memory 間の typed edge を追加 | admin |
| `memory graph list` | memory edge を一覧表示 | admin |

## 目標とするコマンド (v0.14)

```
memory
├── search           # 日常 read（変更なし）
├── show             # 日常 read（変更なし）
├── list             # 日常 read（変更なし）
├── inbox            # candidate のレビュー専用
│   ├── list
│   ├── accept
│   └── reject
├── store            # 意図的な書き込み / 保存系
│   ├── remember
│   ├── propose
│   └── distill
└── admin            # 抽出 + host 連携 + メンテナンス + lifecycle
    ├── extract
    ├── import       # 親グループ (単独では実行不可)
    │   ├── codex
    │   └── instructions
    ├── export
    ├── activate
    ├── hygiene
    │   ├── scan
    │   └── apply
    ├── graph
    │   ├── add
    │   └── list
    ├── supersede
    ├── expire
    └── set-validity
```

### この 3 グループにする理由

- `memory inbox` は candidate レビュー専用に絞る。レビュー待ち一覧と、id 単位での accept / reject だけを置く。durable-memory 行を「書き込む」コマンドは `memory store` に、既存行を「変換する」コマンドは `memory admin` に集約する。
- `memory store` は意図的な書き込み / 保存系を 1 箇所にまとめる。`remember` は accepted 行を書き、`propose` は candidate 行を書き、`distill` は既存 candidate を統合して新しい accepted 行を書く。「durable-memory 行を書き込む」動詞は、結果のライフサイクル状態 (accepted / candidate) によらずすべてここに置く。
- `memory admin` はそれ以外のすべてを集約する。既存セッションから候補を抽出する `extract`、host file との出し入れ (`import` / `export` / `activate`)、メンテナンス (`hygiene` / `graph`)、そして既に保存された行を変更する lifecycle 動詞 (`supersede` / `expire` / `set-validity`)。これらは運用者向けで、日常 read 経路ではない。

### `search` / `show` / `list` を top-level に残す理由

`memory search` と `memory show` は dogfood ログでも各 AI 統合でも最も頻度が高い「日常 read」のコマンドです。AC でもこの 2 つは top-level 据え置きと指定されています。`memory list` も同じ daily-read の役割なので同じ階層に残します。

ただし、後続の sub-issue で実装するときに具体的な阻害要因（たとえば `memory list` のフラグが `memory inbox list` のグループ化と衝突する等）が出れば、そのときに据え置きを再検討して本ドキュメントを更新する想定です。

## マッピング表

| 旧パス | 新しい canonical パス | v0.14 での扱い |
|---|---|---|
| `memory search` | `memory search` | 変更なし |
| `memory show` | `memory show` | 変更なし |
| `memory list` | `memory list` | 変更なし（実装で問題が出たら再検討） |
| `memory inbox list` | `memory inbox list` | 変更なし |
| `memory inbox accept` | `memory inbox accept` | パス変更なし。v0.14 では `--ids` に加えて positional id を受け付け、`--id-only` も追加するため canonical な surface は `memory accept <memory-id>` の strict superset となる (シグネチャの注意は後述) |
| `memory inbox reject` | `memory inbox reject` | パス変更なし。v0.14 では `--ids` に加えて positional id を受け付け、`--id-only` も追加するため canonical な surface は `memory reject <memory-id>` の strict superset となる (シグネチャの注意は後述) |
| `memory accept <memory-id>` | `memory inbox accept <memory-id>` | hidden な deprecated alias (シグネチャの注意は後述) |
| `memory reject <memory-id>` | `memory inbox reject <memory-id>` | hidden な deprecated alias (シグネチャの注意は後述) |
| `memory remember` | `memory store remember` | hidden な deprecated alias |
| `memory propose` | `memory store propose` | hidden な deprecated alias |
| `memory distill` | `memory store distill` | hidden な deprecated alias |
| `memory extract` | `memory admin extract` | hidden な deprecated alias |
| `memory supersede` | `memory admin supersede` | hidden な deprecated alias |
| `memory expire` | `memory admin expire` | hidden な deprecated alias |
| `memory set-validity` | `memory admin set-validity` | hidden な deprecated alias |
| `memory import codex` | `memory admin import codex` | hidden な deprecated alias (シグネチャの注意は後述) |
| `memory import instructions` | `memory admin import instructions` | hidden な deprecated alias (シグネチャの注意は後述) |
| `memory export` | `memory admin export` | hidden な deprecated alias |
| `memory activate` | `memory admin activate` | hidden な deprecated alias |
| `memory hygiene scan` | `memory admin hygiene scan` | hidden な deprecated alias |
| `memory hygiene apply` | `memory admin hygiene apply` | hidden な deprecated alias |
| `memory graph add` | `memory admin graph add` | hidden な deprecated alias |
| `memory graph list` | `memory admin graph list` | hidden な deprecated alias |

## hidden な deprecated alias の扱い

上記で hidden alias となったすべてのパスについて、v0.14 では以下を満たすように残します。

- 動作は v0.13.1 と同じ（挙動変更なし）。
- `Hidden: true` で登録し、`traceary memory --help` には出さない。
- 旧パスを実行したら、stderr に「canonical な置き換えはこちら」という deprecation 通知を 1 行出す。
- stdout / JSON 出力のバイト列は変えない（スクリプトを壊さない）。

これは v0.13 → v0.14 への upgrade で利用者のスクリプト・AI skill・古いドキュメント中のサンプルが silent に壊れないようにするためです。alias は v0.15 で削除します。これは #918 の top-level alias 廃止と同じ「1 リリース猶予」のポリシーに合わせています。

### シグネチャを保持しなければならない箇所

旧 → 新のマッピングのうち、単なる名前替えではなくシグネチャ境界を跨ぐものが 2 種類あります。以降の sub-issue 実装では v0.13.1 の caller contract をそのまま維持しなければなりません。

- `memory accept <memory-id>` / `memory reject <memory-id>` は memory id を **位置引数** (`exactArgsLocalized(1)`) で受け取り、scripted caller 用に `--id-only` を提供しています。一方、現状の `memory inbox accept` / `memory inbox reject` は `--ids` (カンマ区切り、複数指定可) しか受け付けず、`--id-only` もありません。
- `memory import` は親グループであり、**実行可能な leaf は `memory import codex` / `memory import instructions` の 2 つ**です。`traceary memory import` 単独ではコマンドとして動作しません。`memory admin import` を導入する follow-up は、`memory admin import codex` / `memory admin import instructions` を canonical leaf として実装し、旧 leaf 2 つをそれぞれそこへルーティングしてください。親パスだけを差し替えるのは不可です。

これらを script を壊さずに両立させるため、v0.14 では次の方針を取ります。

1. **canonical な `memory inbox accept` / `memory inbox reject` に位置引数と `--id-only` を追加する。** sub-issue #923 で canonical inbox 側に位置引数サポートを追加することが既に決まっています。`--id-only` も canonical 側に追加し、scripts が移行する先のサーフェスが旧 surface のスーパーセットになるようにします。既存の `--ids` バッチフラグは残します。
2. **hidden alias は旧 flag / 旧 arg の形を完全に保つ。** `memory accept <memory-id>` / `memory reject <memory-id>` は位置引数・`--id-only`・`--confidence` (accept のみ)・`--db-path`・`--json` をすべて維持します。deprecation 期間中に新たな required flag を追加してはいけません。
3. **`memory import codex` / `memory import instructions` は現状のフラグをそのまま維持する** (`codex` 側: `--db-path` / `--root` / `--workspace` / `--watch` / `--interval` / `--json`、`instructions` 側: 既存のフラグ集合)。hidden alias は path だけを再ルーティングし、フラグサーフェスは変更しません。

もし follow-up sub-issue でこれらの要件を満たせない事情 (例: canonical inbox 側でフラグ衝突が発生する) が出た場合、その実装 PR は merge **前に** 本ドキュメントを更新してください。merge 後の事後修正にすると deprecation contract の単一情報源が崩れます。

## 削除タイムライン

- v0.14.0: 新しいツリーを導入し、旧パスをすべて hidden deprecated alias として登録、stderr に bilingual な deprecation 通知を出す。
- v0.14.x patch: 表面の追加変更なし。バグ修正のみ。
- v0.15.0: 上記 alias をすべて削除。旧パスは usage error で終了し canonical な置き換えを案内する。#918 / #919 と同じ retirement モデル。

## 本イシューの対象外

- 新しい `memory store` / `memory admin` の Cobra group の実装。
- `presentation/cli` での hidden alias 登録。
- `docs/cli/` / `docs/memory/` のリファレンス更新。
- AI 統合パッケージ (Claude / Codex / Gemini) で memory コマンドを参照している hook / skill 設定の更新。

これらはそれぞれ v0.14 親イシューの別 sub-issue で扱います。本ドキュメントはその共通ベースラインです。

## 関連ドキュメント

- [CLI リファレンス](../cli/README.ja.md)
- [Durable memory ガイド](../memory/README.ja.md)
- [Host-native memory activation contract](../architecture/host-native-memory-activation.ja.md)
