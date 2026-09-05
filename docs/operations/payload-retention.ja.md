# Payload retention / capacity 契約

[English](./payload-retention.md)

Status: 公開 archive package と retention-plan surface は v0.49.0（#2326）で削除されました。このページは今も当てはまる storage の事実だけを残します。

オフライン migration 82 は残っている encoded な event / command-audit
payload を decode し、codec メタデータを落とします。live writer は書いた
ままの plaintext を保存します。compact の encode ステップはなく、圧縮を
再有効化する flag もありません。0.48 ストアは prepared-upgrade candidate
上の migration-only decoder でまだ upgrade できます。

オフライン migration 83 は body-availability / raw-body-retention オブジェクトを
DROP します。本文は書いたまま残り、live な破棄経路はありません。

オフライン migration 84 は空の `archive_segments` を DROP します。非空テーブルは
upgrade を拒否し、0.48.2 の restore/export 手順を出します。この binary は
archive package を読めません。取り出しは Traceary 0.48.2 に pin します。
可搬コピーは `bundle`、安全コピーは `store backup` です。

`store compact --retention-plan` / `--retention-apply` / `--archive*` は
unknown flag です。v0.31 の plan JSON、golden vector、
`schema/retention-plan.schema.json` は live な契約ではありません。

allocated bytes は payload が占める見積もりであり、ファイルシステムがすぐ
回収する約束ではありません。物理サイズが下がるのは `store compact`
（VACUUM INTO / candidate rewrite）または明示 VACUUM のあとです。DROP は
ページを freelist へ移すだけです。
