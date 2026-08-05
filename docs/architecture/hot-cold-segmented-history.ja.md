# Hot/cold 分離による履歴セグメント設計

[English](hot-cold-segmented-history.md)

状態: #1648 の設計チェックポイント。実装は #1649〜#1654 に分割する。

## 決定

Traceary は canonical な event と command audit を全件保持しつつ、書き込み先 Hot SQLite のサイズを制限する。最近の詳細と active/latest/handoff は Hot に残し、古い履歴を immutable・圧縮 SQLite segment に移す。Hot 内の軽量 Segment Catalog が配置と集計情報を管理する。reader は Catalog epoch を固定し、Hot と必要な segment だけを読み、重複なく結果を統合する。

比較した案は、(a) immutable SQLite segment、(b) 独自 append-only bundle、(c) 単一 Cold SQLite である。(a) を採用する。SQLite の検証・query 特性を再利用でき、新しい storage engine を作らずに済むためである。独自 bundle は codec/recovery リスクが大きい。単一 Cold DB は無制限な肥大化と全履歴単位 resume を再発させる。

全履歴 search projection の完走と45分以内の generation は v0.34.0 のリリース条件にしない。段階移行中も既存検索を維持する。

## 要求と非対象

- canonical `events` と `command_audits` を全件保持する。圧縮は表現を変えるだけで、retention は変えない。
- active/latest/handoff と最近の詳細取得を Hot path に保つ。
- resume と検証を segment 単位にする。
- offline candidate、advisory lock、atomic exchange、durable recovery、migration catalog、resource cap、source 不変検証を再利用する。
- copied-store release-gate runner を汎用運用基盤にしない。
- legacy archive/GC/retention deletion を canonical history の第二の authority にしない。

## 概念モデルと不変条件

| 概念 | 状態・振る舞い | 不変条件 |
|---|---|---|
| History Unit | event と optional command audit。event 単位の単調増加 archive sequence を一つ持つ | audit は親 event に従属し、unit の authoritative placement は一つだけ。 |
| Segment | 閉じた sequence 区間、圧縮 payload、index、manifest | seal 後の bytes と論理内容は変更しない。 |
| Catalog epoch | segment metadata と placement の immutable view | 一回の read は一つの epoch を固定する。publication は atomic。 |
| Migration run | 一つの segment を copy、verify、publish、必要なら evict | durable journal の境界ごとに冪等 resume できる。 |
| Authority | publication 前は Hot、placement publication 後は segment | 物理的な重複を論理結果の重複にしない。 |
| Legacy compatibility | 未移行 record は既存 Hot projection で読む | 検証済み coverage より前に旧 projection を削除しない。 |

新規 event の insert transaction で trigger が archive sequence を割り当てる。#1650 は stable store UUID/lineage を作成後、既存 event を bounded/resumable に backfill する。inventory の complete、gap-free、unique が証明されるまで activation しない。optional command audit は親 event と同じ History Unit/segment に配置する。migration は high-water event sequence を固定する。それ以降の late/backdated event はより大きい sequence で後続 segment に入り、segment の time range は重複し得る。building/frozen range 内の既存 event/audit の update/delete は拒否する。v0.34 は cold unit の mutation を扱わず、将来の訂正は append-only で versioned な tombstone/correction record とする。

target は最小の未配置 sequence から始まり、Hot time horizon と max rows、plain bytes、stored bytes、wall time の全条件を満たす最大の連続閉区間とする。大きな segment を作るために sequence を飛ばさない。

Segment ID は `store_id + format version + start sequence + end sequence + logical digest` とし、manifest と Catalog を全要素へ binding する。location は archive root 相対の content-addressed basename に限定し、absolute path、traversal、symlink を拒否する。sealed schema は segment format version のまま immutable とし、Hot migration を適用しない。logical digest は versioned canonical record encoding から計算し、物理破損検出用 file digest と分ける。

## 責務と境界

| 責務 | owner | 境界 |
|---|---|---|
| canonical record/sequence の不変条件 | `domain` | SQLite/file 詳細を持たない。 |
| segment construction と migration orchestration | `application/usecase` | candidate、journal、publication、verification の port。 |
| Catalog snapshot と federated query | `application/queryservice` | consumer-oriented な Hot/segment reader interface。 |
| schema、segment file、lock、exchange、codec | `infrastructure/sqlite` | storage failure を typed application error に変換する。 |
| 明示的な maintenance command と診断 | `presentation/cli` | 通常 read で暗黙 migration しない。 |

Hot store と隣接 archive directory を一つの logical store とする。backup/restore は Hot DB、固定した Catalog snapshot、参照される全 segment manifest/file を一体で扱う。最初の segment publication 後、SQLite-only backup は完全な履歴ではない。

## Segment と Catalog の契約

sealed segment は一つの有界な event-sequence 区間の完全な History Unit、最小限の lookup/order index、segment-local search representation を持つ。format v1 は大きな text/blob に zstd を採用する。長さ付き canonical digest encoding で SQLite storage class、NULL、raw bytes を保存し、decode value は元と一致しなければならない。manifest は format/catalog version、区間、件数、logical/file digest、codec、作成 provenance、集計検証結果を持ち、machine-specific source path は持たない。全 table を `[]map[string]any` と JSON/gzip に materialize する既存 `StoreArchiver` は segment construction に再利用しない。

Catalog は区間、time bounds、件数、state、location、digest、format version、有界な検索候補要約だけを保持し、raw body を複製しない。no-false-negative filter は time overlap、workspace/session/client/agent/kind の exact または Bloom membership、keyed literal n-gram Bloom とする。filter 不可の predicate は time-overlap する全 segment を選ぶ。false positive は許せるが false negative は許さず、exact filtering は source で行う。

状態遷移は `building -> sealed -> verified_shadow -> segment_authoritative -> evicting -> cold` とする。`verified_shadow` までは Hot だけが authority で、segment は parity 専用である。parity 後の短い Catalog transaction が新 epoch と `segment_authoritative` を作る。通常 reader が二つの physical copy を同時に authority として扱う状態は作らない。Hot duplicate は rollback material としてのみ残す。`evicting` は rollback evidence が deletion を許可してから開始し、reclaim 検証後に `cold` へ進む。Catalog placement state と external journal phase は分離し、forward resume しない recovery は `abandoned` または `rolled_back` で終える。

filesystem publish と Hot Catalog transaction は一操作では atomic にできない。segment を fsync し、content-addressed no-replace rename で配置し、directory fsync、durable intent 記録の後に Catalog transaction を commit する。Catalog commit 前の crash は検出可能な orphan を残す。commit 後は journal と観測した Catalog/file state から reconcile する。未検証または欠落 file を Catalog が参照してはならない。

## Federated read と search

reader は Hot read transaction を開始し、現在の Catalog epoch を固定し、その epoch が参照する正確な immutable segment version を開く。`created_at_norm DESC, event_id DESC` で統合する。plan 段階で segment-authoritative interval の Hot row を除外し、post-hoc ID dedupe を authority 判定の代用にしない。continuation は query hash、Catalog epoch、source set、各 source anchor を認証する。v0.34 の epoch は append-only とし、resume 可能な epoch の参照 segment を削除しない。

sessions は Hot canonical のまま残す。各 segment の session aggregate と、late event で transactionally 更新する Hot delta を重複なく合成する。Hot session-resume projection は active lifecycle、latest metadata、有界な recent-command preview、latest compact summary を保持する。v0.34 で CLI `--recent` と MCP context-pack criteria に共通の明示 public max を導入し、projection も同数を保持する。超過は typed validation error、既存 default output は維持する。active/latest/handoff はこの projection を使い、無関係な cold segment が利用不能でも成功する。古い search は安全な Catalog filter の後、候補 segment だけを開く。

必要な segment の欠落・破損時は aggregate diagnostics を伴う明示的な partial/unavailable error を返し、完全な結果であると黙って扱わない。

## Migration、rollback、reclaim

各 run は一つの target interval だけを所有する。

1. 短時間の exclusive store advisory lock 下で source identity、Catalog epoch、target range、high-water を固定する。
2. exclusive lease を解放して cap 下で owned offline candidate へ copy し、seal 前の短い freeze で whole-file SHA ではなく frozen range identity/logical digest を検証する。
3. checkpoint、fsync、seal 後、件数、logical digest、参照、decode parity、source 不変性を検証して `verified_shadow` にする。Hot は唯一の authority のまま。
4. file を配置し、durable intent を reconcile し、router parity 後に短い Catalog transaction で authority を segment へ切り替える。
5. その区間で segment-authoritative と証明された identity だけを削除する。
7. offline Hot candidate を rebuild/compact して atomic exchange し、rollback evidence を保持する。

eviction 前の rollback は interval を Hot placement に戻す新 epoch を publish し、未参照 candidate のみ削除する。eviction 後は sealed segment から offline Hot candidate を再構築・検証・atomic exchange した後、Hot placement epoch を publish する。全 durable boundary の crash から冪等 resume できなければならない。apply/resume/rollback 前には非協調な旧 process と旧 binary を停止する。eviction 後の direct SQLite compatibility は保証しない。

## 既存 maintenance surface

| surface | v0.34 rule |
|---|---|
| event/audit GC、archive `--delete-after-verify`、raw-body retention、dedupe apply/restore | frozen または segment-authoritative ID を含めば typed conflict で fail closed。exact segment ID と Catalog epoch に binding した eviction capability だけが canonical unit を削除できる。 |
| backup/restore | complete-history backup を要求する場面では SQLite-only backup を incomplete として拒否する。 |
| compact | stable Catalog epoch かつ active migration run なしの場合だけ許可する。 |
| legacy search maintenance | Hot compatibility projection だけを更新し、segment authority を変更しない。 |

#1652 は Federated read facade を追加して composition root の read port を差し替える。Hot writer の `EventDatasource` に segment orchestration を詰め込まない。

## 振る舞いテストと release gate

| Given | When | Then |
|---|---|---|
| high-water 固定後の concurrent write | 一区間を migrate | 新 record は Hot に残り、対象 record の loss/duplicate がない。 |
| event/audit の任意 byte | segment decode | decode bytes と logical digest が source と一致する。 |
| file/Catalog publication 前後の crash | resume | authoritative placement が厳密に一つへ復旧する。 |
| 物理 duplicate | federated list/search | canonical identity ごとに一件、stable order で返る。 |
| 選択的な古い query | router plan | false negative なく Catalog 選択 segment だけを開く。 |
| segment 欠落・破損 | complete query | typed incomplete result を返す。 |
| 無関係な cold segment が利用不能 | active/latest/handoff | Hot session-resume projection から完全な結果を返す。 |
| verified segment の eviction/reclaim 後 | rollback | history と source invariants を復元する。 |
| rollout 中の legacy/federated reader | coverage 増加 | observable result の互換性を維持する。 |

v0.34.0 release gate はレビュー済み copy のみを使い、少なくとも一 segment を publish し、crash/resume、実 eviction と physical reclaim、新 router 経由の既存 CLI/MCP list/search/session/active/latest/handoff の before/after parity、rollback を実証し、aggregate-only evidence を出す。全履歴 projection 完走は要求しない。互換対象はこの read surface であり、eviction 後の旧 binary/direct SQLite reader ではない。

## 実装分割と TDD

- #1649: format v1 と codec/validation（activation なし）。
- #1650: schema-only の store lineage、bounded sequence inventory、Catalog/epoch/placement invariant。
- #1651: shadow construction、file publication、journal/recovery（authority cutover なし）。
- #1652: federated read/search、parity、stable pagination、authority cutover（delete なし）。
- #1653: eviction、physical reclaim、post-eviction rollback。
- #1654: CLI、完全 backup/restore、診断、日英 docs。
- #1620: same-head segment gate evidence。#1621: release preparation。

schema migration number は実装時点の shared migration catalog から直列に割り当て、Issue 番号ごとに先取りしない。各 Issue は最新 `main` から開始し、observable behavior test を先に追加する。中止した full-history runner はコピーしない。

## リスクと rollback trigger

- Catalog false negative、mixed-epoch read、digest mismatch、missing segment、source mutation 未証明は publication/eviction を停止する。
- capacity は candidate、WAL、segment、rollback、exchange の headroom を含める。
- compression/encryption/privacy policy は segment ごとに versioning し、log と公開 evidence は aggregate-only にする。
- 最初の rollout では segment read 検証まで Hot duplicate を残す。破壊的 eviction は先行 invariant がすべて証明されるまで release-gate copy に限定する。
