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
| `memory import` | host のメモリを candidate として取り込む | admin / host 連携 |
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
├── inbox            # candidate のレビュー
│   ├── list
│   ├── accept
│   ├── reject
│   ├── propose
│   ├── distill
│   └── extract
├── store            # durable 書き込み + ライフサイクル
│   ├── remember
│   ├── supersede
│   ├── expire
│   └── set-validity
└── admin            # host 連携 + メンテナンス
    ├── import
    ├── export
    ├── activate
    ├── hygiene
    │   ├── scan
    │   └── apply
    └── graph
        ├── add
        └── list
```

### この 3 グループにする理由

- `memory inbox` は既に candidate レビューの namespace になっており、独立していた `accept` / `reject` / `propose` / `distill` / `extract` を吸収するのが自然。
- `memory store` は durable な書き込み面。新しい memory を記録する、置き換える、expire する、validity window を変える、と durable 層を実際に書き換える操作だけをまとめる。
- `memory admin` は運用・host 連携寄りのコマンド。host file との出し入れ、Claude / Codex / Gemini への activation 計画、hygiene スキャン、graph 操作はいずれも日常 read ではなく、これまで個別に top-level に並んでいた。

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
| `memory inbox accept` | `memory inbox accept` | 変更なし |
| `memory inbox reject` | `memory inbox reject` | 変更なし |
| `memory accept` | `memory inbox accept` | hidden な deprecated alias |
| `memory reject` | `memory inbox reject` | hidden な deprecated alias |
| `memory propose` | `memory inbox propose` | hidden な deprecated alias |
| `memory distill` | `memory inbox distill` | hidden な deprecated alias |
| `memory extract` | `memory inbox extract` | hidden な deprecated alias |
| `memory remember` | `memory store remember` | hidden な deprecated alias |
| `memory supersede` | `memory store supersede` | hidden な deprecated alias |
| `memory expire` | `memory store expire` | hidden な deprecated alias |
| `memory set-validity` | `memory store set-validity` | hidden な deprecated alias |
| `memory import` | `memory admin import` | hidden な deprecated alias |
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
