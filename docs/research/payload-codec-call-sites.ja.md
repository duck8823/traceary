# 決定: 残る `payloadCodecIdentity` 呼び出し (#1779)

[English](./payload-codec-call-sites.md)

**Status:** 決定済み。

**Date:** 2026-08-15

**Issue:** #1779

## 決定

| 箇所 | 決定 | 理由 |
|---|---|---|
| Bundle import（`bundle_datasource.go`） | `encodeCanonicalPayload` を採用 | import は live store への書き込み。bundle 形式は plaintext のまま。writer は native hook 書き込みより store を膨らませない。 |
| Archive restore（`store_archive.go`） | `encodeCanonicalPayload` を採用 | archive export は既に decode して plaintext を出す。restore は書き込み。compatibility counter は実際に保存した codec に従う。 |
| Dedupe restore（`content_event_dedupe_datasource.go`） | `encodeCanonicalPayload` を採用 | quarantine archive は decode 済み plaintext。restore が identity で再符号化すると store が膨らむ。import と同じ writer 規則。 |
| Raw-body recovery（`raw_body_retention_datasource.go` の restore） | `encodeCanonicalPayload` を採用 | recovery body は ledger の plaintext。restore は書き込み。 |
| Retention marker（raw-body apply と `discardEligibleEventBodies`） | identity のまま | 短い sentinel。apply/verify は stored TEXT を `EventBodyUnavailableRetentionMarker` と比較する。canonical は no-op か、その比較を壊す。 |

## 対象外

- bundle / archive 形式の変更
- live store の照会
