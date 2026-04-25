# Optional[T] API 移行ポリシー

[English](./optional-api.md)

Traceary は `duck8823/dotfiles/conventions/go/type-system.md` で文書化された Go 規約より前に独自の `Optional[T]` API を使っていた。
このドキュメントはギャップ・目標 API・移行ロールアウトを記録する。

## 現状

`domain/types.Optional[T]` は規約エントリポイントのみを公開している:

- `types.Some(...)`
- `types.None[T]()`
- `Value()`
- `OrElse(...)`

レガシー互換 alias (`Of`, `Empty`, `IsPresent`, `Get`) は **v0.10 で削除済み**。リポジトリは規約サーフェスに完全移行した。

## 規約目標

このリポジトリで追跡している Go 規約は以下を推奨する:

- `types.Some(...)`
- `types.None[T]()`
- `Value()`

Traceary はこの規約に完全準拠している。

## 移行履歴

### フェーズ 1: 互換サーフェス追加

レガシー API を残したまま、規約エントリポイント (`Some`, `None`, `Value`) を追加した。意味論は変更なし。

### フェーズ 2: リポジトリ呼び出し側の移行

呼び出し側を以下のバッチで移行した:

- `domain/` と `application/`
- `infrastructure/`
- `presentation/`
- テストとヘルパー

### フェーズ 3: レガシー名削除 (v0.10)

`Of`, `Empty`, `IsPresent`, `Get` を削除した。pre-1.0 のクリーンアップで、互換 shim は残していない。

## 新規コードのルール

```go
opt := types.Some(value)
none := types.None[string]()
value, ok := opt.Value()
```

- 新規コードでは `Some`, `None`, `Value` を使う
- レガシー名を再導入しない

## Non-goals

このポリシーは以下を意味しない:

- Optional の意味論変更
- すべてを pointer に置き換える

## 関連ドキュメント

- [アーキテクチャ原則](./README.md)
- [ドキュメントインデックス](../README.md)
