# Hot/cold 分離による履歴セグメント設計

[English](hot-cold-segmented-history.md)

状態: v0.34.0 向けに簡素化した決定。実装は #1649〜#1654 と整理 Issue #1669 に分割する。

## 決定

Traceary は最近のデータを writable な Hot SQLite に残し、古い履歴を immutable・圧縮 SQLite segment へコピーする。Hot 内の軽量 Catalog が候補 segment を示す。古い履歴の reader は Hot と選択された segment を検索し、stable event ID で重複を除去する。

archive 処理は意図的に at-least-once とする。並行 read は一時的に partial になり得て、同じ record が Hot と segment の両方に物理的に存在してよい。v0.34.0 では DB 全体の snapshot、exactly-once publication、generation 全体の resume、eviction 後の rollback を保証しない。

active/latest/handoff は Hot-only のままとし、archive segment を開かない。

## 最小限の不変条件

- publish 後の segment は変更しない。
- segment file の完全な書き込み、fsync、install が終わる前に Catalog から参照しない。
- durable な segment を Catalog が参照する前に Hot record を削除しない。
- Hot と segment の重複は event ID で一件にまとめる。
- 必要な segment の欠落・破損は partial とし、完全な結果だと扱わない。
- 通常運用では canonical events と command audits を保持し、圧縮は表現だけを変える。

## 構成要素

| 構成要素 | 責務 |
|---|---|
| Hot SQLite | 最近の events/audits、sessions、active/latest/handoff、Catalog。 |
| Segment writer | 一つの有界な閉区間を圧縮 immutable segment へコピーし、読み戻しを検証する。 |
| Catalog | publish 済み segment ごとに一行: content-addressed basename、sequence/time 範囲、件数、digest を保持する。 |
| Search router（将来、#1652） | Hot と Catalog が選んだ segment を検索し、stable order で統合、ID dedupe、partial coverage を報告する。 |
| Evictor（将来、#1653） | durable な Catalog entry が覆う Hot identity だけを削除する。物理 compaction は通常 maintenance とする。 |

v0.34.0 の経路では offline Hot candidate、DB 全体の atomic exchange、durable migration-run journal、rollback state machine、全履歴 projection generation を使わない。

## Archive flow

1. 現在の Hot 内容から古い有界 range を選ぶ。後続の並行 write は Hot に残る。
2. private temporary file に segment を構築する。
3. manifest、件数、digest、decode を検証する。
4. file を fsyncし、atomic rename で install し、directory を fsyncする。
5. 冪等な Catalog entry を追加または確認する。registration 自体が install 済み sealed file を manifest と照合して再検証し、directory を再 fsync するため、durable な install より前に Catalog row は作られない。
6. 後続 pass で対応する Hot ID を削除する。crash で重複が残っても reader が dedupe する。

step 5 より前に crash した temporary/orphan file は破棄できる。step 5 後、step 6 前に crash すると Hot と segment が安全に重複する。再実行は segment 単位で始め、durable な step 別 workflow は持たない。

## Search behavior

最近の read は Hot だけを query する。古い literal search は Catalog の sequence/time 範囲で segment を選び、候補だけを開き、各 source で exact filtering して既存の event order で統合する。time 範囲が不完全な segment は false negative を避けるため選択する。

利用不能・破損した候補は aggregate diagnostics へ数え、結果を partial にする。並行 archive により request 間で coverage が変化してよく、v0.34.0 は request をまたぐ global snapshot を保証しない。

## 観測可能な振る舞いテスト

- publish 済み segment を読み戻せて manifest/digest が正しい。
- durable な segment install より前に Catalog publish しない。
- durable segment coverage のない ID を eviction 対象にしない。
- Hot と segment の重複を event ID ごとに一件へまとめる。
- 選択された segment の欠落・破損を partial とする。
- active/latest/handoff が segment を開かない。
- 主要な既存 CLI/MCP search の互換性を維持する。

DB 全体の atomic exchange、durable migration phase、全 crash boundary の厳密 resume、eviction 後の再構築、generation 全体の projection に関するテストは採用設計の対象外であり、実装と同時に削除する。

## 実装分割

- #1649: immutable 圧縮 segment format と validation。
- #1669: 不採用にした厳密 migration protocol の削除と、#1650 Catalog の最小 registration table への縮小。store 初期化は、不採用の未リリース catalog が記録した migration ledger を、置き換え後の Catalog migration を黙って skip せず fail closed で拒否する。
- #1652: 最小 Hot/segment search と ID dedupe。
- #1653: coverage がある ID の eviction と通常の space reclaim。
- #1654: 最小限の運用と文書。
- #1620、#1621: 代表的な振る舞いの検証と release preparation。

この設計を汎用 migration framework に拡張しない。より強い snapshot、recovery、rollback 保証は後続 Issue の対象であり、v0.34.0 の release condition ではない。
