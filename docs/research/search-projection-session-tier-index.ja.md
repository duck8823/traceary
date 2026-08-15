# session ティアの index 対 LIKE（#1756）

[English](./search-projection-session-tier-index.md)

session ティアに unicode61+porter の FTS5 が要るかの scratch 測定です。live の約 36 GiB store は対象外です。

## 決定

**exact-keyword + `LIKE` を残します。** session ティアの FTS は足しません。予算外の第 2 index も、新しいフラグも足しません。

`SearchSessionPage` はこれまでどおり `search_projection_session_keywords` の exact match、または `summary_text LIKE '%q%' ESCAPE '\'` です。

## 理由

1. Issue の stemming 例は LIKE の取りこぼしではありません。`LIKE '%deploy%'` は `deployed` に当たります。
2. 比較用 index は `--index-family-bytes` を消費します。この scratch では 290,816 バイトで、残りの family（352,256）と同じ桁です。1464 MiB 天井ではその分 recent 窓が縮みます。
3. ラベル付き relevant 集合では LIKE の recall は porter FTS より悪くありません。FTS が勝つのは精度（`undeploy` は substring の余分）とダイアクリティカル（`cafe` ↔ `café`）です。family 予算の index を払う理由には足りません。

## scratch コーパス（`TestSessionTierKeepsLikePathAndMeasuresPorterFTS`）

session 要約 2,009 件（ラベル 9 + filler 2,000）。クエリ集合は既存の session-tier テストと Issue の品質ケースです。porter FTS は比較テーブル（`search_projection_session_compare_fts`）として作り、捨てます。スキーマに `search_projection_session_*fts*` は残りません。

| クエリ | LIKE hits | LIKE recall | LIKE p50 | LIKE p95 | FTS hits | FTS recall | FTS p50 | FTS p95 |
|---|---|---|---|---|---|---|---|---|
| unique-session-marker | sess-unique | 1.00 | 2.54 ms | 2.73 ms | sess-unique | 1.00 | 62 µs | 68 µs |
| filter-needle | sess-filter | 1.00 | 2.44 ms | 2.52 ms | sess-filter | 1.00 | 9 µs | 10 µs |
| subsecond-marker | sess-subsecond | 1.00 | 2.51 ms | 2.68 ms | sess-subsecond | 1.00 | 9 µs | 9 µs |
| deploy | deployed, deploy, DEPLOY, **undeploy** | 1.00 | 2.42 ms | 2.53 ms | deployed, deploy, DEPLOY | 1.00 | 9 µs | 10 µs |
| Deploy | deploy と同じ | 1.00 | 2.44 ms | 2.77 ms | deploy と同じ（undeploy なし） | 1.00 | 9 µs | 9 µs |
| cafe | cafe のみ | 1.00 | 2.41 ms | 2.75 ms | cafe + café | 1.00 | 8 µs | 9 µs |
| café | café のみ | 1.00 | 2.40 ms | 2.63 ms | café + cafe | 1.00 | 9 µs | 11 µs |

`deploy` の relevant は `{deployed, deploy, DEPLOY}` です。LIKE の extra は `undeploy`（substring）です。`cafe` / `café` の shipped path は keyword と同じ ASCII fold だけです。

| 時点 | dbstat |
|---|---|
| 比較 FTS 前の family | 352,256 |
| 比較 porter FTS（shadow 含む） | **290,816** |
| drop 後の family | 352,256 |

1464 MiB 天井ではこの 290,816 バイトは family が recent 窓に回せない分です。多年の session ティアでは index は小さくならず大きくなります。ここでの LIKE レイテンシは 2,009 行の走査であり 36 GiB store ではありません。その走査を買うために予算外 index は足しません。

## 約束

session ティアは exact-keyword + LIKE のままです。後の minor で index を足すなら `--index-family-bytes` に含めます。
