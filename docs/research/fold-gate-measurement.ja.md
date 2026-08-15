# 決定: v0.34 で測れなかった fold / wake 行を測る (#1879)

[English](./fold-gate-measurement.md)

**Status:** 決定済み。

**Date:** 2026-08-15

**Issue:** #1879

## fold する価値のある session

次のどちらかなら **worth folding** です。

1. `session_refinements` 行がある
2. `SUM(event_metadata_projection.body_stored_bytes) >= consolidation.threshold_bytes`（既定 64 KiB）

(1) は fold 済みで後から body を捨てた session を残す。(2) は stop-hook の ask が発火するのと同じ閾値超えです。

`refinement_ratio = refinement_count / worth_folding_count`。v0.34 の gate は `>= 0.95`。

## wake injection

集計ではなく `sessions.client` ごと。適格は top-level session + `has_agent_reasoning = 1`（#1877 の規則）。host が **injects** なのは、適格 summary が 1 件でも `wake_injection.budget_bytes`（既定 8 KiB）に収まるとき。Antigravity は injection 対象外のまま。client が無いのは fail ではなく `unmeasured`。

## summary の中身

#1874 は動機と変更を書けと ask した。この harness はそれを意味解析しない。最新 20 件の agent-authored summary を sample し、nonempty / mechanical-template / `content_proxy_ok`（nonempty、mechanical header ではない、40 バイト以上）を出す。

## live store

計測は明示の `--db` コピーが必須。既定 live path は拒否する。v0.34.0 タグ時点の reference store の最後の正直な数字は **refinement 0 / session 27,552 / wake 適格 0**。scratch fixture は経路の証明であり、その corpus ではない。

## 対象外

- consolidation / wake の振る舞い変更

#1873 はこれらの計測を fixture store 上のリリースゲートとして評価します。[リリースゲート](../release/gates.ja.md) を参照してください。
