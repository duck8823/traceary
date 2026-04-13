# Optional[T] API の移行方針

[English](./optional-api.md)

Traceary では、`duck8823/dotfiles/conventions/go/type-system.md` にある Go 規約より前から使っている独自の `Optional[T]` API が残っています。
この文書では、その差分、目標 API、そして unrelated な機能変更に repo-wide refactor を混ぜずに移行する方針を整理します。

## 現在の Traceary の状態

`domain/types.Optional[T]` は現在、次を公開しています。

- `types.Of(...)`
- `types.Empty[T]()`
- `IsPresent()`
- `Get()`
- `OrElse(...)`

この API は domain / application / infrastructure / presentation / test に広く使われています。
この方針を書いた時点でも、legacy 名の参照は repository 全体で数百件規模あり、一括 rename の PR はノイズもリスクも大きすぎます。

## 規約上の target API

この repository が参照している Go 規約では、次を推奨しています。

- `types.Some(...)`
- `types.None[T]()`
- `Value()`

Traceary の方針は、この規約 API へ寄せることです。
つまり、恒久的なローカル例外にするのではなく、移行を選びます。

## 方針

Traceary は、段階的に規約 API へ移行します。

### 目指す最終形

新しいコードは、次の形で読める状態を目指します。

```go
opt := types.Some(value)
none := types.None[string]()
value, ok := opt.Value()
```

### 過渡期の扱い

unrelated な作業を止めないために、移行には明示的な compatibility window を設けます。

1. まず規約 API (`Some`, `None`, `Value`) を追加する
2. call site は review しやすい単位で順に移す
3. repository 内の移行が終わった段階で、legacy 名を削除するか、互換 alias として残すかを明示的に決める

## 推奨 rollout

### Phase 1: compatibility surface を足す

まず、意味を変えずに次を追加します。

- `Some`
- `None`
- `Value`

この段階では、call site を全部まとめて変えません。

### Phase 2: repository 内の call site を順に移す

移行は、たとえば次のような単位で分けます。

- `domain/` と `application/`
- `infrastructure/`
- `presentation/`
- test / helper

粒度は調整してよいですが、各 PR は review 可能な大きさに保ちます。

### Phase 3: legacy 名の扱いを確定する

repository 側の移行が終わったら、次を明示的に決めます。

- `Of`, `Empty`, `IsPresent`, `Get` を削除する
- もしくは、ローカル互換 alias として残す

ここは曖昧にしません。repo 全体が半端に混在した状態を放置しないためです。

## 移行中の新規コードに対するルール

移行が終わるまでの間は、次をルールにします。

- 規約 API が入った後の新規コードでは、原則 `Some`, `None`, `Value` を使う
- ただし、まだ移行していないファイルを小さく直すだけの PR では、その場の一貫性を優先して legacy 名を維持してもよい
- Optional API cleanup を、対象外の bug fix や feature PR に混ぜない

## 非対象

この方針は、次を意味しません。

- Optional の意味自体を変えること
- `Optional[T]` を pointer に全面置換すること
- 1 本の巨大 PR で repo 全体を rename し切ること

## 関連文書

- [アーキテクチャ原則](./README.ja.md)
- [ドキュメント索引](../README.ja.md)
