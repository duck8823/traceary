# `--recent-age` の bind 測定 (#1755)

[English](./search-projection-recent-age.md)

recent ティアのどちらが bind するかの scratch サイズ測定です。live の約 36 GiB ストアではありません。

## 決定

**`--recent-age` は残します。** 第 3 のフラグは足しません。削除するなら admin の 1 minor deprecation window が要ります。

retention は `created_at > max(now − age, byteCutoff)` です（`RecentRetentionCutoff` / `PlanProjectionBatch`）。

## scratch コーパス（`TestRecentAgeBindingOnScratchCorpora`）

`now = 2026-06-30T00:00:00Z`、age = 30 日（age cutoff = 2026-05-31）。

| 形 | `RecentCutoffNorm` | bind | 残る行 |
|---|---|---|---|
| 密な ingest | 2026-06-20 | **byte** | 2026-06-25 残す; 2026-06-10 捨てる（age だけなら残る）; 2026-05-20 捨てる |
| 静かなストア | 空（walk が交差しない） | **age** | 2026-06-15 残す; 2026-05-20 捨てる |

容量圧があるストアでは byte cutoff のほうが新しいので、30 日フラグは窓を縮めません。age が bind するのは recent ティアがもともと小さいときだけです。`--recent-age` を触るのは、index-family 予算以外の理由で静かなストアの古い行を落とす場合です。
