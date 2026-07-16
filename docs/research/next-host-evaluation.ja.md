# 決定: 次ホスト評価 — Copilot CLI vs opencode (#1256)

[English](./next-host-evaluation.md)

**Status:** v0.26.0 向け評価完了 — **この issue では host 実装なし**  
**Date:** 2026-07-16  
**Issue:** #1256

## 決定

- **次ホスト候補の第一候補:** GitHub Copilot CLI（hook surface が文書化され CLI でサポート）。
- #1256 / v0.26.0 内では Copilot CLI も opencode も**実装しない**。実装は後続マイルストーンで package / doctor / contract 計画後に専用 child issue へ分割する。
- **opencode:** 二次ウォッチ。plugin/event モデルはあるが Traceary の shell-hook 包装とは異なり、`application/hostcoverage` に対する sanitized live probe が先。

## 一次情報

### GitHub Copilot CLI

- Hooks reference: https://docs.github.com/en/copilot/reference/hooks-reference
- Using hooks: https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-hooks
- Tutorial: https://docs.github.com/en/copilot/tutorials/copilot-cli-hooks

### opencode

- Plugins / events: https://opencode.ai/docs/plugins/

## v0.25 host 枠組みとの対照

| 観点 | Copilot CLI | opencode |
|---|---|---|
| 文書化された hook / lifecycle | あり | plugin event（別モデル） |
| marketplace 秘密なしの local install | repo/user hooks JSON で可能と見込まれる | TypeScript plugin 経路 |
| `scripts/hooks` + hostcoverage 適合 | 高い | 中（adapter 必要） |
| 評価 PR での実装リスク | 同梱すると高い | 同梱すると高い |

## Follow-ups（v0.26.0 外）

1. Copilot CLI hook の sanitized live probe → fixture と hostcoverage 行。
2. packaging + doctor の設計メモ。
3. probe 通過後の専用実装 issue（1 issue = 1 PR）。

## 確認した Non-goals

- 評価 PR での新規 host package 出荷。
- live fixture なしの lifecycle 完全互換主張。
