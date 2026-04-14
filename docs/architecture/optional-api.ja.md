# Optional[T] API の移行方針

[English](./optional-api.md)

Traceary には、`duck8823/dotfiles/conventions/go/type-system.md` にある Go 規約より前から使っていた独自の `Optional[T]` API がありました。
この文書では、その差分、目標 API、そして unrelated な機能変更に repo-wide refactor を混ぜずに repository を規約側へ寄せた移行方針を整理します。

## 現在の Traceary の状態

`domain/types.Optional[T]` は現在、規約側の entrypoint として次を公開しています。

- `types.Some(...)`
- `types.None[T]()`
- `Value()`

そのうえで、legacy surface との互換のために、次も残しています。

- `types.Of(...)`
- `types.Empty[T]()`
- `IsPresent()`
- `Get()`
- `OrElse(...)`

repository 内のコードは、すでに規約側の entrypoint へ移行済みです。
legacy 名は、downstream caller をすぐに壊さないための compatibility alias としてだけ残しています。

## 規約上の target API

この repository が参照している Go 規約では、次を推奨しています。

- `types.Some(...)`
- `types.None[T]()`
- `Value()`

Traceary の方針は、この規約 API へ寄せることです。
つまり、恒久的なローカル例外にするのではなく、移行を選びます。

## 方針

Traceary は、段階的に規約 API へ移行しました。

### 目指す最終形

新しいコードは、次の形で読める状態を目指します。

```go
opt := types.Some(value)
none := types.None[string]()
value, ok := opt.Value()
```

### 過渡期の扱い

unrelated な作業を止めないために、移行では明示的な compatibility window を設けました。

1. まず規約 API (`Some`, `None`, `Value`) を追加する
2. repository 内の call site を review しやすい単位で順に移す
3. repository 内の移行が終わった段階で、legacy 名は compatibility alias として当面残す、と明示的に決める

## 推奨 rollout

### Phase 1: compatibility surface を足す

まず、意味を変えずに次を追加しました。

- `Some`
- `None`
- `Value`

この段階では、call site を全部まとめて変えませんでした。

### Phase 2: repository 内の call site を順に移す

移行は、review 可能な大きさを保ちながら進めました。たとえば、次のような単位が考えられます。

- `domain/` と `application/`
- `infrastructure/`
- `presentation/`
- test / helper

粒度は調整してよいですが、規約側 API を既定にすることが目的です。

### Phase 3: legacy 名の扱いを確定する

repository 側の移行が終わった現時点では、次の方針にしています。

- `Of`, `Empty`, `IsPresent`, `Get` は compatibility alias として当面残す
- 削除するなら、別 issue で明示的に扱う

ここは曖昧にしません。repo 全体が半端に混在した状態へ戻らないためです。

## 移行中の新規コードに対するルール

現在のルールは次です。

- 新しいコードでは `Some`, `None`, `Value` を使う
- `Of`, `Empty`, `IsPresent`, `Get` を新規 call site に増やさない
- legacy 名は compatibility shim であって、通常の記法ではないと扱う

## 非対象

この方針は、次を意味しません。

- Optional の意味自体を変えること
- `Optional[T]` を pointer に全面置換すること
- 1 本の巨大 PR で repo 全体を rename し切ること

## 関連文書

- [アーキテクチャ原則](./README.ja.md)
- [ドキュメント索引](../README.ja.md)
