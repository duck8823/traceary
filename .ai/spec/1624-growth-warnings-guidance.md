# Issue #1624 — growth warnings and safe maintenance guidance

## Structure-Behavior Design Note

### Requirement summary
- 目的: doctor の増大警告を固定DBサイズだけでなく、metadata-onlyのpayload/projection容量、filesystem headroom、実測診断latencyから判断し、安全なpreview-first maintenanceへ案内する。
- 現状: 1 GiBのfile sizeだけで警告し、GC以後の安全なscrub/compact/swap/rollbackと中断復旧が案内されない。
- 期待する振る舞い: いずれかの独立signalが予算超過ならwarning。hintは常にnon-destructive previewを最初にし、#1613 safe compactionへ誘導する。
- 非対象: 自動GC/compact、payload内容の読取り、v0.35 activation、閾値のremote設定。

### Conceptual model
| Concept | State | Behavior | Constraint / Invariant |
|---|---|---|---|
| Growth evidence | DB/event-payload/projection/reclaimable/filesystem-free/latency | independent signalsを評価 | event payloadはevent_metadata_projection aggregate。audit payloadは含むと主張しない |
| Growth warning | pass/warn/unavailable | preview-first hintを生成 | mutationしない |
| Safe maintenance | copy→preflight→scrub→compact→swap→rollback | operator guidance | in-place VACUUM禁止 |

### Responsibility assignment
| Responsibility | Owner | Reason to change | Not owner / reason |
|---|---|---|---|
| aggregate容量取得 | CapacityInspector | SQLite metadata schema | CLIはSQLを持たない |
| filesystem free取得 | CLI platform adapter | OS差分 | evaluatorはplatform非依存 |
| threshold/evidence評価 | doctor_store_size | operator policy | datasourceはseverityを決めない |
| runbook | bilingual docs | operation lifecycle | warning messageへ全手順を埋めない |

### Boundaries / interfaces
| Boundary or interface | Consumer | Hidden detail | Error contract |
|---|---|---|---|
| CapacityInspector | doctor | PRAGMA/dbstat/aggregate query | unavailableはsize-onlyへ縮退 |
| inspectDoctorDiskFree | doctor | Statfs | errorはsignal unavailable |
| evaluateStoreGrowth | tests/doctor | thresholds | deterministic pass/warn |

### Behavior tests
| Behavior | Given | When | Then | Level |
|---|---|---|---|---|
| payload warning | small DB, large payload aggregate | evaluate | warn + preview first | unit |
| projection warning | large projection | evaluate | warn | unit |
| headroom warning | free < compaction working set | evaluate | warn | unit |
| latency warning | capacity inspectionのopen開始から全aggregate完了までがslow | evaluate | warn | unit |
| healthy | all budgets below | evaluate | pass | unit |
| privacy | capacity report | doctor | values are aggregates only | integration |

### TDD plan
| Behavior | Red | Green | Refactor target |
|---|---|---|---|
| multi-signal thresholds | table tests fail fixed-size evaluator | evidence evaluator | signal helpers |
| preview-first remediation | hint ordering assertion | safe command sequence | localized constants |
| docs safety | forbidden in-place VACUUM grep | bilingual runbook | shared headings |

### Risks / rollback
- 手続き化リスク: CLIがSQLを持たないようCapacityInspectorを再利用する。
- premature abstraction: public config/strategyは追加しない。
- migration / compatibility: schema/API変更なし。capacity unavailable時は既存size signalへ縮退。
- rollback trigger: doctor latency/lock regression、またはwarning誤検知が通常運用を阻害。
- 分割案: evaluator+tests、CLI wiring、bilingual docs。1 issue内のcoherent changeとして1 PRに統合。
- design checkpoint: metadata-only aggregate以外を読まず、mutation commandを自動実行しないことを実装前条件とする。
- large-store checkpoint: 2 GiB以上の既定doctorはcapacity inspectorより先に分岐し、SQLite openを0回にする。詳細signalはreview済みcopyで取得する。
- latency definition: capacity inspectorのopen開始からPRAGMA/dbstat/event-payload aggregate完了まで。warning 1.5s、hard timeout 2s。
- free-space model: availabilityとvalueを分離し、取得成功時の0/1/threshold以下はwarning、unsupported/errorだけunknownとする。DB×2はsaturating計算する。
- stat snapshot: doctorはDB pathを1回だけstatし、同じimmutable snapshotでregular/size/large/reportを決める。直後のdelete/renameでもpanicやSQLite openへfallbackしない。
- message contract: Statfs成功0 bytesは`free=0 B`、unsupported/errorは`free=unknown`。signed rangeと乗算overflowを事前拒否する。

### Self-review checklist
- [x] 固定DB sizeだけでseverityを決めない
- [x] FixCommandはsafe `store compact plan --db-path` preview（GCではない）
- [x] content/identifierをmessageへ出さない
- [x] unsafe in-place VACUUMを推奨しない
- [x] v0.35 activationは明示的にdefer
