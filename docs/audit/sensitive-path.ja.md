# Sensitive path 検出

[English](./sensitive-path.md)

Sensitive-path 検出は、dotenv・SSH 鍵・cloud credentials・browser profile・
鍵材料など高リスクな場所に触れたように見える command audit を flag します。
次の claim とは**別**です:

- **Secret redaction** — 保存 payload の値をマスクする（`application/redaction`）
- **Host capture coverage** — hook 証跡が信頼できるほど完全か

## 一致の意味

一致は、audit 材料の中に **sensitive な intent または path 文字列** が観測
されたことを意味します。それだけでは OS がファイルを開いたことの証明にはなり
ません。

| Evidence | 意味 |
|---|---|
| `command_text_only` | shell / command テキストが一致（例: `cat .env`）。intent は観測。file access は未証明 |
| `structured_file_tool` | host の file tool payload に path が含まれる（例: Read/Write）。より強い path claim |
| `unresolved_path` | パターンは一致したが path をきれいに解決できない |

Coverage（`complete` / `partial` / `unobservable`）は payload 品質
（truncate、空 body、command のみ）を表し、sensitivity そのものではありません。

## 既定パターン class

実装は `application/sensitivepath`:

- `dotenv` — `.env`、`.env.*`
- `ssh_key` — `~/.ssh`、`id_rsa` / `id_ed25519` basename
- `cloud_creds` — `~/.aws`、credentials / service-account JSON 名
- `browser_profile` — Chrome / Firefox / Brave の profile / cookie path
- `key_material` — `*.pem` / `*.key` / `*.p12`、keychain path
- `custom` — 追加パターン（`extra_redact_patterns` と同系統。設定配線は今後拡張可）

## CLI

```sh
# 一致した command audit のみ（event body の compute-on-read）
traceary list --sensitive --kind audit --limit 20

# 詳細 JSON の command_audit に `sensitive` object が付く
traceary show <event-id> --json
```

## doctor

`traceary doctor` の `sensitive-access-audit` は直近の一致を要約し、intent-only
や弱い coverage のとき WARN します。検出は受動的で、blocking / deny/ask はしません。

## Non-goals

- hook のリアルタイム allow/deny
- host が command テキストしか出さない場合の OS レベルの open 証明
