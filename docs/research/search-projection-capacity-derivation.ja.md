# search-projection の容量導出 (#1751)

[English](./search-projection-capacity-derivation.md)

scratch サイズでの 5 項目の測定です。live の約 36 GiB store は対象外です。

| 項目 | 判定 | scratch での大きさ | 出荷 |
|---|---|---|---|
| 1. 再導出が再試行されない | 実在 | `source→eviction` commit 後の crash で Start 時推定が残る | `capacity_rederived` と eviction apply ごとの再試行 |
| 2. dbstat と `SUM(decoded_bytes)` が別 snapshot | 実在 | 並行 DELETE で PPM が膨張（physical-before / logical-after） | 読み取り専用 transaction 1 本 |
| 3. 世代混合 + FTS 削除 posting のラチェット | 混合: 追加クエリでは PPM が変わらない（FTS 共有）。ラチェット: reclaim 前に再導出すると実在 | 未マージの削除は inverse posting を残す。reclaim で recent バイトは増えない | 再導出の前に reclaim。世代単位の physical 走査はしない |
| 4. 予算超過完了にアクチュエータがない | 検出だけが実在 | status は `0` を記録済み。CatchUp は `already_complete` のまま | `doctor` の `search-projection-budget` 警告。自動 rebuild はしない |
| 5. 天井の上方修正が prefilter 落ちを再投影できない | 無視できる | 走査天井は Start 天井の 4 倍。scratch の Start/再導出差は 4 倍未満 | 再投影パスは作らない |

ファミリバイト予算は今も **測定して報告** するだけで、事前保証ではありません。この変更は導出過程を再試行可能・snapshot 一貫にするだけで、再構築ピークは上限しません。
