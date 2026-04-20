# アーキテクチャ原則

[English](./README.md)

この文書は、Traceary のソフトウェアアーキテクチャについて守るべき原則を整理したものです。とくに hook runtime のような構造変更を進めるときに、「なんとなくそうしていた」ではなく、明文化された境界に沿ってレビューできるようにすることを目的としています。

## この文書が必要な理由

Traceary はすでに 4 層構造を採っていますが、runtime の振る舞いと補助スクリプトの境界が一部で曖昧です。もっとも分かりやすい例が、現在の hook 配布方式です。`scripts/hooks/` 配下には互換用の shell wrapper が残っていますが、Traceary の主 runtime はあくまで Go です。

この方式は今のところ機能しています。ただし、将来にわたって維持したい理想形ではありません。この文書では、その理想形を定義します。

## 4 層構造

Traceary では、次の 4 層を前提にします。

```text
presentation -> application -> domain <- infrastructure
```

| 層 | 置くもの | 置かないもの |
| --- | --- | --- |
| `presentation/` | CLI コマンドの配線、MCP server の handler、hook ホスト固有の payload 解釈、利用者向けの表示整形、transport ごとの入力検証 | domain ルール、永続化の実装、長期的な業務状態 |
| `application/` | write-side use case、read-side query service の契約、domain object をまたぐオーケストレーション、共有の read model / DTO | SQL、filesystem の細かな操作、transport 固有の payload 解釈 |
| `domain/` | entity、value object、repository 契約、不変条件、業務エラー | Cobra、SQLite、JSON transport の扱い、shell 連携の都合 |
| `infrastructure/` | SQLite 実装、filesystem adapter、プラットフォーム固有の file handling、外部依存の adapter | domain/application に置くべき業務判断、その場しのぎの CLI 制御 |

### 依存方向

- `presentation` は `application` と `domain/types` に依存してよい
- `application` は `domain` に依存してよい
- `domain` は他の層に依存してはいけない
- `infrastructure` は `application` の契約と `domain` に依存してよいが、内側の層から `infrastructure` を参照してはいけない

ここで目指しているのは、何でも抽象化することではありません。業務ルールと runtime entrypoint の置き場所を、誰が見ても分かるようにすることです。

## runtime の振る舞いはどこに置くか

runtime の本体ロジックは、明示的な例外がない限り、通常の Go package に置きます。場当たり的な helper script を primary な実装にしてはいけません。

### 推奨する runtime entrypoint

利用者やホストから直接呼ばれる entrypoint は `presentation/` に置きます。

例:
- `traceary` CLI コマンド
- `traceary mcp-server`
- 今後追加する `traceary hook ...` サブコマンド

これらの entrypoint では、ホスト固有の payload やユーザー入力を受け取り、正規化したうえで application の use case へ渡します。

### hook ホスト adapter

hook ホスト adapter は、ホスト固有の runtime payload を解釈する限り、presentation 層の責務です。

つまり、次は presentation に置きます。

- Claude / Codex / Gemini ごとの event 名や payload 形式の差
- hook invocation の stdin / env / JSON の解釈
- それらを application 入力へ正規化する処理

逆に、host ごとの shell 振る舞いを runtime 本体として抱え続けるべきではありません。

### 外向きの integration asset

hook 設定ファイルや互換用 shell wrapper のように、利用者環境へ配布・展開する asset は残る場合があります。これらは runtime の正本ではなく、配布・導入の都合として扱います。したがって、責務としては `infrastructure/` 側です。

判断に迷ったら、次の 2 問で分けます。

- **「ユーザーが Traceary を呼び出したとき、何が起きるべきか」** → `presentation` / `application`
- **「ホストが Traceary を呼べるように、どうファイルを配置・生成するか」** → `infrastructure`

## `scripts/` の役割

`scripts/` は、開発・運用・リリース補助のための置き場です。

典型例:
- 検証スクリプト
- リリース準備用ユーティリティ
- packaging / install の補助
- CI 向けの保守スクリプト

`scripts/` は、長く残る production runtime logic の本体置き場ではありません。

もし runtime 時にも必要な script が残るなら、それは一時的な互換レイヤーとして扱い、最低限次を明記します。

- なぜまだ必要なのか
- 本来の Go entrypoint は何か、または何になる予定か
- その縮小・削除を追跡している issue はどれか

## 現在の例外: hook 用 shell asset

現状では、`scripts/hooks/` 配下の shell wrapper を、互換用の packaged integration asset として同梱しています。これは明示的な一時例外です。

進めたい方向は次の通りです。

1. hook runtime の本体を Go サブコマンド（`traceary hook ...`）へ移す
2. shell asset が必要なら、薄い互換 wrapper だけを残す
3. 最終的に `scripts/hooks/` を packaged hook asset の正本とみなさない状態へ移る

この移行が終わるまでは、`scripts/hooks/` は互換用 infrastructure とみなし、新しい業務ロジックや runtime の主実装をそこへ足さないのが原則です。

## `internal/` についての方針

`internal/` は可視性を制御するための仕組みであって、アーキテクチャそのものではありません。

Traceary では、`internal/` を既定では要求しません。

使うのは、次のような場合だけです。

- その package が実装詳細であり、外から import されると明確に害がある
- 可視性を制限した方が public package surface を実質的に単純化できる
- その制約が、既存の層名・package 名よりも明快である

単に見た目をきれいにするためだけに `internal/` を増やしてはいけません。境界が曖昧なら、まず境界を直します。

## 新しい設計を足すときの確認項目

新機能や refactor を入れるときは、少なくとも次を確認します。

1. runtime の本体ロジックは helper script ではなく Go package にあるか
2. transport / host 固有の解釈は `presentation/` に留まっているか
3. オーケストレーションは `application/` にあるか
4. domain から SQLite / CLI / MCP の都合が見えていないか
5. `infrastructure/` は契約を実装しているだけで、業務判断を作っていないか
6. script が残る場合、それは helper か一時的な互換レイヤーだと明示されているか
7. `internal/` を提案するなら、好みではなく具体的な可視性上の理由があるか

どれかが満たせない場合は、例外を明文化してからマージします。

## 関連文書

- [ドキュメント索引](../README.ja.md)
- [Optional API 移行方針](./optional-api.ja.md)
- [Memory blocks: 評価と決定](./memory-blocks.ja.md)
- [Hook contract](../hooks/contract.ja.md)
- [イベントライフサイクル](../lifecycle.ja.md)
- [ネイティブ連携ガイド](../integrations/README.ja.md)
- [Durable memory ガイド](../memory/README.ja.md)
