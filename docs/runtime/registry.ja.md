# 共有headless registry backend

[English](registry.md)

Issue #2472として[R0](../adr/2026-11-03-headless-runtime-r0.ja.md)のregistry backendを実装する。
共有application基盤であり、Desktop依存や新public CLI commandではない。
Daemon wiringとoperator commandは各実装Issueで接続する。
metadata登録ではagent process、native hook、model request、core SQLite migration、host設定を変更しない。

## 責務と利用

- `domain/runtime.Registry`：project/worktree所属、固定identity、重複/上限、防御的snapshot、明示delete plan。
- `application/runtime.Registry`：RegisterProject、RegisterWorktree、List、Export、Resolve、Revalidate、PlanDeleteProject/Worktree、ApplyDelete。
- `infrastructure/runtime.RegistryResolver`：canonical Git worktree/common-dir、現owner、file種別、group/world writable拒否。Git plumbingは有界contextを使い、`GIT_*`選択/config変数を継承しない。
- `infrastructure/runtime.RegistryStore`：明示private rootのversion 1 `registry.json`を永続化し、core storeとは分離する。

登録時にsymlink aliasをcanonical化する。
projectは登録rootとGit common-dirにowner/device/inodeの物理witnessを含めて識別する。
worktreeは所属Git rootと既存のowned regular store fileを固定する。
Git root以外、foreign common-dir、消失/置換identity、unsafe modeは拒否する。
store本文はDBとしてopenせず、変更もしない。identityだけを調べる。

Targetはworktree snapshotと明示profile IDの組である。
Claude/Codexが同じworktreeを独立に選択でき、globalなprofile選択状態を設けない。
ここではprofile参照の構文だけを検証し、Daemon起動前の実executable/version検証はprofile所有者が担当する。
利用直前に保存targetと期待profileをRevalidateする。
registry metadataはinput/stop認可ではなく、spawn/controlのfilesystem raceを単独で閉じるものでもない。
runtime所有者が操作境界を維持する。
record削除で既存Target値や実行中processを変更しない。

## 永続化と失敗時の挙動

metadata rootはcomponentごとにsymlinkを辿らずopenする。
祖先はroot/current UID所有かつgroup/world writableなしを要求する（root所有sticky一時directoryを除く）。
最終rootはcurrent UID所有、0700とし、既存unsafe modeを黙って修復しない。
root物理identityを固定し、path置換時はinstanceを無効にする。
metadata/lockはcurrent UID所有、0600、単一linkのregular fileとする。
symlink/FIFO/deviceを辿らず拒否する。

固定サイズのprocess内lock stripeとOS file lockで、別instance/processを直列化する。
lock待ちはcancel可能とする。
rename前の失敗/cancelでは前のenvelopeを残す。
排他的random一時file、file sync、同directory内atomic rename、directory syncを使う。
rename後のdirectory-sync失敗は`ErrCommitUncertain`で返し、未変更と決め付けずreload/照合する。
黙ってretryしない。

strict decodeで未知field、重複key、非canonicalなfield大小文字、不正UTF-8、必須fieldの欠落/null、余剰JSON、偽identity、不正所属、サイズ超過、未知versionを拒否し、上書きしない。
上限はproject 100、worktree 1,000、registry metadata最大128 MiBである。
constructorのbudgetは厳しくするだけで、上限を拡大できない。
他runtime envelope導入時はDaemon compositionでR0の合計metadata予算も強制する。
現backendはregistry envelopeと空lock fileだけを書き込む。

Exportは防御的application snapshotを返し、transport consumerがaccessorから型付きDTOへ変換する。
削除は正確なrevision/contentに固定したpreviewを要する。
planを放棄/cancelしても変更せず、registry変更で旧planを失効させる。
worktree登録中のprojectは削除できない。
ApplyDeleteはmetadataだけを削除し、Git worktree/branch、store、terminal出力、host履歴を消さない。
実行中resourceの削除方針は後続runtime所有者が担当し、registryが自動cleanupしない。

## 検証と制限

public API testでcanonical alias、linked/foreign repo、別profile選択、stale target/store置換、両entry上限、snapshot隔離、preview/cancel/delete、不正/新版metadata、symlink/mode、budget保護、cancel、別instance/実process同時更新を検証する。
fixtureは破棄可能なlocal repo/store fileだけを使う。

native host/resume認定や、悪意ある同一UID processからの隔離の証明ではない。
異UID/ACLとIPC/process controlの統合は別runtime検証とする。
このbackendにUI/PTY/native model実行は不要である。
rollbackはregistry mutationを止め、metadataを保持/exportし、ユーザーresourceを強制cleanupしない。
