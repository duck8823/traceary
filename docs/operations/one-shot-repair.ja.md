# 証拠に基づく完結型セッション修復

[English](./one-shot-repair.md)

`traceary session repair-one-shot` は、監督対象プロセスは完了したものの終了境界を記録できなかった過去セッションを修復する。`session gc` より対象を厳しく限定し、セッションごとの明示的なプロセス終了証拠と型付き終了理由を必須にする。transcript 本文、idle 時間、workspace 所属、end hook の欠落だけから完結型実行を推測しない。

## 証拠 manifest

コマンドは `one-shot-repair-evidence/v1` schema を受け付ける。

```json
{
  "schema_version": "one-shot-repair-evidence/v1",
  "entries": [
    {
      "session_id": "session-example",
      "runtime_mode": "one_shot",
      "terminal_reason": "success",
      "completed_at": "2026-07-21T10:00:00Z",
      "evidence_source": "operator_attested_process_exit",
      "evidence_ref": "batch-run:42"
    }
  ]
}
```

各 entry は `runtime_mode=one_shot` を明示し、レガシー値ではない終了理由、完了時刻、権威あるプロセス終了証拠を指定する。証拠種別は次のいずれかに限る。

- `supervised_process_exit`
- `codex_exec_exit`
- `batch_runner_exit`
- `operator_attested_process_exit`

証拠参照はレビュー用出力に表示するが、event body にはコピーしない。run ID や SHA-256 digest など、秘密情報を含まない安定した値を使う。

1 回の実行で受け付ける entry は最大 50,000 件である。session ID は 1,024 byte、証拠参照は 4,096 byte までとし、それ以上の修復はレビュー済み batch に分割する。manifest ファイル自体の上限は 64 MiB である。

## 先に dry-run する

既定動作は dry-run である。

```sh
traceary session repair-one-shot \
  --evidence-file ./one-shot-evidence.json \
  --stale-after 24h \
  --json > one-shot-repair-preview.json
```

結果にはストア全体の変更前後件数（`active`、`stale`、成功した `completed`、型付きの `failed`）と、manifest の全 entry に対する説明が含まれる。主な判定は次のとおり。

| 判定 | 意味 |
|---|---|
| `eligible` | active かつ stale で、完了時刻より新しい保存済み activity がない |
| `missing_session` | manifest の ID が存在しない |
| `already_terminal` | すでに終了済みで、再実行しても変更しない |
| `recently_active` | `--stale-after` の期間内に activity がある |
| `completion_before_start` | 証拠の時刻がセッション開始より前 |
| `completion_before_latest_activity` | 提案した完了より後の activity があり、証拠と矛盾する |

過去 migration で `interactive` として保存された行を自動選択しない。正確な session ID を権威ある one-shot manifest に含めた場合だけ対象候補になる。manifest にないセッションは変更しない。

## 適用と rollback

適用には新しい backup path が必要である。

```sh
traceary session repair-one-shot \
  --evidence-file ./one-shot-evidence.json \
  --stale-after 24h \
  --apply \
  --backup ./traceary-before-one-shot-repair.db \
  --json > one-shot-repair-applied.json
```

application usecase は backup path を必須入力とし、backup、ストア初期化、適用の全順序を管理する。backup を省略できる公開 application apply 経路はない。Traceary はストアの初期化および修復 transaction より前に backup を作成する。dry-run は既存データベースを read-only で開き、ストアの新規作成や migration を行わない。対象変更と終了 event はすべて同じ transaction で commit する。エラーまたは commit 前の中断では実行全体を rollback する。成功後に同じ証拠を再実行すると `already_terminal` になり、重複 event は追加しない。

commit 済み修復を戻す場合は Traceary の writer を停止し、必須 backup を復元する。

```sh
traceary store backup restore \
  --input ./traceary-before-one-shot-repair.db \
  --db-path ~/.config/traceary/traceary.db \
  --force --yes
```

復元はストア全体の snapshot を置き換えるため、backup 後に記録された event も削除される。maintenance window で実施し、適用時の JSON は監査記録として保持する。

activity と終了 writer の並行動作に関する release QA は、[v0.30.0 完結型セッション修復の並行書き込み dogfood](../release/v0.30.0-one-shot-repair-concurrency-dogfood.ja.md) に記録している。
