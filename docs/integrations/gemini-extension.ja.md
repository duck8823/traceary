# Gemini CLI extension

[English](./gemini-extension.md)

> **v0.21.0 — レガシー注記:** Gemini CLI はレガシーの Google AI エージェントホストであり、Traceary のアクティブな委譲パスではなくなっています。後継ホストは **Antigravity** です。Antigravity の capability detection は #1195 で実装済みですが、サポートされた公開 CLI/hook contract が確認されていないため、v0.21.0 では Antigravity の hook / package / release asset を**意図的に提供しません**（#1196 で判断）。このページは既に Gemini CLI を使用している既存インストール向けに Gemini extension package を説明します。Antigravity サポートの現状は [Antigravity 移行状況](./antigravity.ja.md) を参照してください。

Gemini 向け package は `integrations/gemini-extension/` にあります。Gemini CLI は install された extension の root に `gemini-extension.json` があることを前提にするため、Traceary では tagged release ごとにこの package を専用 archive として配布します。

## 自動で組み込むもの

- `traceary mcp-server` を使う `traceary` MCP server
- `SessionStart` / `SessionEnd` hook
- `BeforeAgent` prompt hook（送信された user prompt を `prompt` event として記録）
- `AfterAgent` transcript hook（agent の応答を `transcript` event として記録）
- `run_shell_command` 向け `AfterTool` audit hook
- `PreCompress` compact marker hook（Gemini に post-compress summary hook がないため、圧縮前の境界だけを記録）
- slash command の `/traceary-help` と `/traceary-doctor`
- 文脈で効く `traceary-session-history` / `traceary-memory-review` / `traceary-memory-remember` skill。`traceary-memory-review` は review 意図の発話 (「Traceary inbox」「review memory candidates」「session recap」など) で発火し inbox の curate を案内、`traceary-memory-remember` は明示 write 発話 (「覚えておいて」「remember that」など) のみで発火します。

## Memory activation strategy

Gemini integration は、Traceary の accepted memory store を MCP tools・instruction-file export・host-native activation 経由で利用できます。review 済み memory を Gemini instructions に見せる方法は次の 2 通りです。

**Option 1 — instruction-file export (引き続き利用可能)**: review 済み memory を `GEMINI.md` の Traceary 管理ブロックへ export します。

```sh
traceary memory admin export --target gemini --out GEMINI.md
```

**Option 2 — host-native activation (v0.13.0+、project 推奨)**: `traceary memory admin activate --target gemini` で `GEMINI.md` 内の小さな import stub と `.traceary/memories/gemini.md` の external memory file を管理します。activation pair は管理領域外の user-authored 内容を保持し、symlink / directory / malformed marker / newer marker 等の不安全 target を拒否し、idempotent です。Traceary は `save_memory` が生成する `## Gemini Added Memories` セクションを管理・書き換えません。そのセクションは Gemini auto-memory tool の所有物で、通常の host-context content として保持されます。セクションが既に存在する場合、Traceary は managed import stub をファイル末尾に append するため、両者は安全に共存します。Gemini 用 smoke test は `--apply` 後も seed した `## Gemini Added Memories` が byte-for-byte で保持されることを検査します。

```sh
# host pair を読み取り専用で確認
traceary memory admin activate --target gemini --status

# 計画の確認 (dry-run、書き込みなし)
traceary memory admin activate --target gemini --dry-run --diff

# 安全な per-file write で pair を反映（idempotent）
traceary memory admin activate --target gemini --apply
```

既定値:

- activation root: 直近の `.git` 祖先、なければ cwd
- host context file: `<root>/GEMINI.md`
- external memory file: `<root>/.traceary/memories/gemini.md`
- `GEMINI.md` に書き込む import 行: `@./.traceary/memories/gemini.md`

`--root <dir>` / `--path <file>` で上書き可能です。managed marker layout・status state・tracked-file policy などの全契約は v0.13 host-native memory activation [ADR](../architecture/host-native-memory-activation.ja.md) を参照してください。`invalid` からの復旧は [durable memory ガイド](../memory/README.ja.md#invalid-からの復旧)にまとめています。`traceary doctor --client gemini` には同じ dry-run / apply 再実行 command を持つ `gemini-memory-activation` check が surface されます。

## Install

1. 先に Traceary CLI を入れます。

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
# または
GO111MODULE=on go install github.com/duck8823/traceary@latest
```

2. Traceary の GitHub release から extension を install します。

```sh
gemini extensions install https://github.com/duck8823/traceary --ref <tag>
```

Traceary では、archive root が Gemini CLI の期待する extension root になる `traceary.tar.gz` asset を release ごとに公開します。

この repository を使ってローカル開発したい場合は、代わりに link を使います。

```sh
gemini extensions link integrations/gemini-extension
```

## Update

```sh
gemini extensions update traceary
```

特定の release に戻したい場合は、`--ref <tag>` を付けて再 install します。

## Uninstall

```sh
gemini extensions uninstall traceary
```

## Doctor と smoke test

実運用の確認は次を基本にします。

```sh
traceary doctor --client gemini --json
```

`doctor` は Gemini capture について次の 2 つを確認します。

- `gemini-config`: Traceary 管理の hook が一部だけ（例: 旧式の
  SessionStart / SessionEnd / AfterTool のみ）になっている場合に警告します。
  settings.json へ入れている場合は `traceary doctor --client gemini --fix`
  で修復できます。
- `gemini-event-coverage`: recent Gemini session を見て、prompt/transcript が欠けた
  session 比率が `--coverage-threshold`（既定 `0.5`）を超えると警告します。audit のみの session も会話内容の coverage がないため警告対象です。
  settings.json ではなく Gemini extension package を使っている場合は、
  `gemini extensions update traceary` で package 側の BeforeAgent /
  AfterAgent hook を更新してください。

package 自体の validate は次です。

```sh
gemini extensions validate integrations/gemini-extension
```

この repository からの end-to-end smoke test は次です。

```sh
TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1 ./scripts/smoke_test_integrations.sh gemini
```

この opt-in 環境変数は意図的です。Gemini CLI は browser 認証 prompt を開くことがあるため、既定の `./scripts/smoke_test_integrations.sh all` は headless な release-prep shell ではこの runtime probe を skip します。
