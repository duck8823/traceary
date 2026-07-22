# archive / backup の容量保持ポリシー

[English](file-capacity-retention.md) | [日本語]

Traceary v0.31 では、ローカルの archive root と SQLite backup root を、期間・件数・割り当て byte 数で制限する明示的な plan/apply 手順を追加します。この機能は自動ではなく、live database の compaction も行いません。

## 安全性の契約

- `plan` は読み取り専用です。明示した plan 出力ファイル以外へ書き込みません。
- archive と backup は、別々の root と予算として判定します。
- ファイルがある class では、現在の store 系統に一致する検証済み recovery floor が必要です。一致する最新の recovery point を保護します。
- 読み取れない entry、symbolic link、hard link、device 境界、不正な予約 manifest、byte 上限に必要な割り当て量が不明な entry がある場合、その class は `indeterminate` になり、apply batch は空になります。
- 破損、未検証、orphan、pin 済みのファイルは表示され、既知の容量圧力にも含まれますが、削除候補にはなりません。
- 各候補は、root の device/inode、ファイルの device/inode/link count、size、mtime、SHA-256、検証証拠、全理由、1件単位の順序付き batch に固定されます。
- `apply` には plan ID の完全一致が必要です。残っている inventory 全体と recovery floor を再確認します。plan の既定有効期間は1時間です。
- 削除では root の排他 lock、永続 catalog/journal/ledger、同一 directory 内の上書き禁止 hard-link tombstone を使います。再試行は既知の状態だけを再開し、既存 tombstone を上書きしません。

新しく作成した SQLite backup には、digest と source store 系統を固定する予約 retention manifest も作成します。この manifest がない既存 backup は `backup_manifest_missing` として報告され、削除を許可する証拠にはなりません。store archive は内部 manifest と table digest を検証します。暗号化 archive は、passphrase 対応 verifier を追加するまでは報告専用です。

## plan の作成

v0.31 の dogfooding 中は hidden command として提供します。

```sh
traceary store retention files plan \
  --db-path ~/.config/traceary/traceary.db \
  --archive-root ~/.config/traceary/archives \
  --archive-max-age 2160h \
  --archive-max-count 12 \
  --archive-max-allocated-bytes 1073741824 \
  --backup-root ~/.config/traceary/backups \
  --backup-max-age 720h \
  --backup-max-count 8 \
  --backup-max-allocated-bytes 2147483648 \
  --output /tmp/traceary-file-retention-plan.json
```

`canonical_payload.classes[].inventory`、`floor`、`status`、`ceilings`、`candidates`、`batches` を確認してください。`display` は実行判定に使いません。`status: satisfied` の class だけを apply できます。

## apply と再試行

```sh
PLAN_ID=$(jq -r .plan_id /tmp/traceary-file-retention-plan.json)
traceary store retention files apply \
  --plan /tmp/traceary-file-retention-plan.json \
  --confirm-plan-id "$PLAN_ID"
```

処理が中断した場合は plan を残し、1分の fenced lease が切れた後に同じ command を再実行します。`.traceary-retention-*` の state file を改名・編集しないでください。成功した再試行では、各候補が `committed`、対応する ledger が1行、catalog が `deleted` に収束します。

## recovery の検証

実データに cleanup policy を適用する前に、保持された floor を使い捨て store へコピーし、次を確認します。

```sh
sqlite3 copied-backup.db 'PRAGMA integrity_check;'
traceary store archive verify --input retained.trcaryar
```

その後、使い捨ての Traceary database へ復元し、代表的な metadata read と full-body read を実行します。容量 cleanup と SQLite compaction は別の操作です。
