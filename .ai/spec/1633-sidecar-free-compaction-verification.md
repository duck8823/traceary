# Issue 1633: sidecar-free compaction verification

## Structure-Behavior Design Note

### Requirement summary
- 目的: compaction自身のSQLite検証がWAL/SHMを生成してswap guardを壊す自己干渉をなくす。
- 現状: direct read-only接続が通常のSQLite read-only DSNを使い、WAL-mode DBでzero-byte sidecarを作り得る。
- 期待する振る舞い: source/candidateのcompatibility・scrubとVACUUM元読取をimmutable接続で行い、sidecarを生成せずone-shot applyをcommitする。
- 非対象: pre-existing sidecarの削除、sidecar ownership推測、guardの緩和。

### Conceptual model
| Concept | State | Behavior | Constraint / Invariant |
|---|---|---|---|
| Verification snapshot | immutable main DB file | compatibility and logical scrub | sidecarを作成・参照しない |
| Sidecar guard | absent / pre-existing | swapを許可または拒否 | zero-byteでも自動削除しない |
| Compaction run | prepared → verified → committed | resume/rollback可能 | 検証がfile setを変えない |

### Responsibility assignment
| Responsibility | Owner | Reason to change | Not owner / reason |
|---|---|---|---|
| direct SQLite open mode | `compaction_sqlite.go` | SQLite接続副作用 | usecaseはDSNを知らない |
| sidecar fail-closed | `compaction_files.go` | filesystem replacement invariant | verifierは削除しない |
| workflow state | compaction usecase/domain | resume/rollback | infraは状態遷移を決めない |

### Boundaries / interfaces
| Boundary | Consumer | Hidden detail | Error contract |
|---|---|---|---|
| `SQLiteCompactionBuilder` | compaction usecase | immutable DSN, scrub | compatibility/SQLite errorを文脈付きで返す |
| `StoreReplacementFiles` | compaction usecase | inode/sidecar inspection | pre-existing sidecarはfail closed |

### Behavior tests / TDD plan
| Behavior | Red | Green | Refactor target |
|---|---|---|---|
| VerifyPair leaves no sidecars | WAL-mode source/candidateの検証後sidecar不在 | immutable direct open | shared immutable opener |
| one-shot apply commits | apply直後のguardが自己生成sidecarで失敗 | build/verifyをsidecar-free化 | DSN責務のみ変更 |
| pre-existing sidecar rejects | zero/nonzero sidecarを配置 | guard維持 | 自動cleanup禁止 |
| resume/rollback remain safe | verified/swap_intent journalから再開 | existing transitions維持 | なし |

### Risks / rollback
- immutableはmain DBだけを読むため、事前guardでsidecar不在を必須とする。
- migrationなし。問題時はdirect openerを旧DSNへ戻せる。
- 分割: behavior testsと接続修正を1 issue/1 PRで行う。
- design checkpoint: sidecar削除ではなくsidecar非生成を採用する。

### Self-review
- guardを緩和せず、zero-byte sidecarも削除しない。
- source/candidateへ同じcompatibility/scrubを適用する。
