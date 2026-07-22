# v0.31 retention copied-store dogfooding

[English](./retention-dogfood-v0.31.md)

状態: Issue #1445 の dogfooding は 2026-07-22 に完了しました。release blocker の follow-up #1486 は後述のとおり追跡し、release 前に close します。以下の path はすべて `/private/tmp` 配下の使い捨て copy または synthetic root です。live store の唯一の copy は対象にしていません。

## 決定

- raw-body の prune/restore と archive/backup capacity の plan/apply command は v0.31 で公開しますが、手動 opt-in のままです。
- install、update、hook、`doctor`、通常の read command は retention plan を作成・適用しません。
- automatic archive-before-GC は `retention.mode=archive_then_gc` による別の opt-in 機能で、既定値は `disabled` のままです。
- SQLite compaction は別操作のままで、どちらの retention apply も実行しません。
- metadata と aggregate は v0.31 retention executor の物理削除対象にしません。

## raw-body drill

使い捨て database に 56 byte の body 1件と event metadata 1件を用意しました。plan の前に、その event を含む検証済み・非暗号化 archive を作成しました。

| 観測項目 | apply 前 | apply 後 | restore 後 |
|---|---:|---:|---:|
| body logical bytes | 56 | unavailable (`body_unavailable_reason=retention`) | 56 |
| SQLite logical file size | 262,144 | 262,144 | 262,144 |
| SQLite allocated bytes | 262,144 | 262,144 | 262,144 |
| event/session/source metadata | baseline | retention lifecycle field 以外は byte-equivalent | 元の値 |
| `PRAGMA integrity_check` | `ok` | apply test で `ok` | `ok` |

証拠:

- Plan `3ba7a0ef48b0fffc89258281ac265d4dbb60f80ac291629074e78a9f0409185d` は exact event identity を `age` 理由で選び、recovery digest `927d130e00e98586fb5caace14803f7170df1df82c10a593715bef895fd63db1` を固定しました。
- 間違った confirmation は失敗し、database の SHA-256 は不変でした。
- exact apply は body 1件を prune し、再実行は already-pruned 1件となって二重変更しませんでした。
- full-body JSON は空 message と `body_unavailable_reason=retention` を返し、body-free の identity、time、client、agent、session、workspace は不変でした。
- restore は1件を復元し、再実行は already-restored 1件でした。元の56 byte payload と full-body query が戻り、`PRAGMA integrity_check` は `ok` でした。
- `TestRawBodyRetention_interruptionResumesFromDurableBatch` は durable batch 後の中断を注入し、retry が resumed 1件・already-durable 1件へ収束することを検証します。

allocated size が変わらないのは想定どおりです。logical body pruning は SQLite page の即時解放を保証せず、retention は compaction を実行しません。

## archive / backup drill

各使い捨て root は、現在の store generation に一致する検証済み file 2件から開始しました。count ceiling `1` は古い file を選び、新しい recovery floor を保護しました。

| class | apply 前 logical / allocated | apply 後 logical / allocated | 保持した floor |
|---|---:|---:|---|
| archive | 1,678 / 8,192 bytes | 839 / 4,096 bytes | `new.trcaryar` |
| backup | 524,288 / 524,288 bytes | 262,144 / 262,144 bytes | `new.db` |

証拠:

- Plan `aae96eb0b69f4690418ffc2711b0c6bdfe3236ea3fcf779305352282d2f140f3` は `old.trcaryar` と `old.db` を `count` 理由で選びました。
- 間違った confirmation の前後で root evidence hash は不変でした。
- exact apply は候補2件を conflict なしで削除し、再実行は already-committed 2件でした。
- 削除対象 backup の sidecar はなくなり、保持 floor の sidecar は残りました。
- raw-body recovery archive の SHA-256 `927d130e00e98586fb5caace14803f7170df1df82c10a593715bef895fd63db1` を `store archive verify` で検証し、新しい使い捨て database へ復元しました。dry-run と apply は `inserted=1 skipped=0 conflicts=0 total=1` を返しました。
- archive の復元先は `PRAGMA integrity_check=ok` となり、metadata query で event/session/kind/client/agent/workspace の完全一致と利用可能な56 byte bodyを確認し、`traceary show` で元の全文を確認しました。
- 保持した SQLite backup も copied database へ復元し、代表 event count `1` と `PRAGMA integrity_check=ok` を確認しました。
- parameterized file-retention crash matrix は全 journal/namespace boundary を対象にし、retry が committed journal 1件、完全一致 ledger row 1件、deleted catalog state、candidate/tombstone 不在、floor 存在へ収束することを検証します。

## doctor/status の証拠

`traceary doctor --archive-root ... --backup-root ... --json` の capacity-root 検査部分は読み取り専用です。retention plan を作成・適用せず、保持した root を Database section に表示しました。ただし `doctor` command 全体は読み取り専用ではありません。指定した SQLite database の初期化・migration を行い、`--fix` は capacity 検査と無関係な review 済み修復を実行できます。

- archive: `state=ready files=1 verified=1 logical_bytes=839 allocated_bytes=4096 floor=new.trcaryar`
- backup: `state=ready files=1 verified=1 logical_bytes=262144 allocated_bytes=262144 floor=new.db`
- どちらも automatic cleanup が無効であることと、手動 plan command を明示しました。

dogfooding では、最初の status 実装が group/other-writable root を `ready` と表示できる一方、exact apply は同じ permission boundary を拒否する不一致を発見しました。release blocker として follow-up #1486 を登録し、v0.31 release 前に修正します。

## rollback と残存リスク

rollback は reviewed restore を使い捨て database へ実行し、integrity check と代表 query で確認します。QA drill では live database を置換しません。file capacity は保護した verified floor、raw-body は digest-pinned recovery archive を使います。

filesystem contract は caller UID で動く process を信頼します。apply は caller 所有でない root と group/other-writable root を拒否し、Traceary writer は root lock を共有します。portable Unix には inode 条件付き unlink がなく、同じ UID は Traceary database/config 自体を変更できるため、悪意ある same-UID process は対象外です。
