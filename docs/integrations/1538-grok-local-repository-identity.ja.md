# 設計メモ: Grok local-repository identity の移行 (#1538)

[English](./1538-grok-local-repository-identity.md)

## 問題

Grok Build は local checkout を repository scope で導入します。
`#integrations/grok-plugin` subdirectory selector を付けないと、host が
`traceary-grok` package 名ではなく repository identity を登録することがあります。
後から canonical package を導入しようとしても、すでに導入済みとして拒否されます。

## 境界と判断

plugin list には別の概念が二つあります。

| 概念 | source | 用途 |
| --- | --- | --- |
| package 名 | manifest package entry | canonical `traceary-grok` の識別 |
| local-repository identity | `repo_key` と local `source` の inventory metadata | 旧 repository-scope install の識別 |

doctor が読むのはこの inventory metadata と hook contract metadata だけです。
導入済み plugin file、prompt、transcript、credential、plugin payload は検査しません。

既定の installer は破壊的に動作しません。現在の checkout の plugin subdirectory が
non-canonical な local identity として登録されている場合、exit 78 で停止します。
明示的な `--migrate-local-repo-identity` はその source の完全一致だけを削除し、
`repository#integrations/grok-plugin` を導入します。別 source の legacy package は
選択も削除もされません。

## 観測可能な受け入れテスト

- clean home は subdirectory selector 経由で canonical package を導入する。
- canonical package の refresh は `traceary-grok` だけを置き換える。
- 旧 local-repository identity は uninstall せずに停止する。
- 明示的な移行は source が完全一致する local identity だけを削除し、canonical hook、MCP、skill へ収束する。
- 別 source の legacy `traceary` package はそのまま残る。
