# 決定: ストレージゲートを合成コーパスで校正する (#1811)

[English](./storage-gate-calibration.md)

**Status:** 決定済み。

**Date:** 2026-08-15

**Issue:** #1811

## 決定

v0.34 の #1620 数値は maintainer の 1 ストアだった。corpus 形状に依存する
gate 行は、`go run ./cmd/store-benchmark --calibrate-gates DIR` が出す
**範囲** にする。

| コーパス | 次元 |
|---|---|
| `tiny` | 短い一意レコード多数（page slack） |
| `enormous` | 大きなレコード少数（行あたり予算 / 除外） |
| `cjk` | マルチバイト・trigram 密度 |
| `entropy` | 一意性が高い（hash） |
| `repetitive` | 圧縮 / dedupe の最良側 |

生成は決定的（seed `1811`）。sample data は commit しない。writer は
production の `EventDatasource.Save`（canonical payload codec）。計測は
`CapacityInspector` と `OperatorCostInspector`（`traceary store capacity` と
`traceary doctor` と同じ）。

search-index amplification（`deriveSearchProjectionCapacity`、fallback 2.16x）
は、completed な search-projection generation が無い限りこの harness では
`unmeasured`。rebuild 経路は recent-tier sample が 8 MiB 未満だと実測比を
拒否する。それは operator 向けベンチマークであり、既定の `go test` ではない。

## 対象外

- live store の照会
- 5 コーパス job を `go test ./...` に入れること
- この Issue で fallback 2.16x 定数を変えること
