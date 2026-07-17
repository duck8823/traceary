package cli

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/duck8823/traceary/presentation"
)

// inspectArchiveRetention reports automatic archive-before-GC status (#1372).
// Fail-closed: default mode is disabled. Never prints secrets.
func (c *RootCLI) inspectArchiveRetention(ctx context.Context, dbPath string) doctorCheck {
	_ = ctx
	const name = "archive-retention"
	cfg := presentation.LoadConfig().Retention
	mode := strings.TrimSpace(cfg.Mode)
	if mode == "" {
		mode = presentation.RetentionModeDisabled
	}

	if !strings.EqualFold(mode, presentation.RetentionModeArchiveThenGC) {
		return doctorCheck{
			Name:   name,
			Status: doctorStatusPass,
			Message: localizef(
				"automatic archive-before-GC is disabled (retention.mode=%s); manual path: traceary store archive create",
				"automatic archive-before-GC は無効です (retention.mode=%s); 手動: traceary store archive create",
				mode,
			),
			Hint: Localize(
				"opt in via config.json retention.mode=archive_then_gc (passphrase via env name only)",
				"config.json の retention.mode=archive_then_gc で opt-in（passphrase は env 名のみ）",
			),
		}
	}

	interval, err := parseArchiveAutoInterval(cfg.Interval)
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("archive_then_gc enabled but interval is invalid: %v", "archive_then_gc が有効ですが interval が不正です: %v", err),
			Hint:    Localize("set retention.archive_then_gc.interval to a Go duration such as 168h", "retention.archive_then_gc.interval に 168h のような Go duration を設定"),
		}
	}

	if env := strings.TrimSpace(cfg.PassphraseEnv); env != "" {
		if _, ok := os.LookupEnv(env); !ok {
			return doctorCheck{
				Name:   name,
				Status: doctorStatusWarn,
				Message: localizef(
					"archive_then_gc enabled but passphrase env %s is not set; automatic runs fail closed (no delete)",
					"archive_then_gc が有効ですが passphrase env %s が未設定です。自動実行は fail-closed（削除なし）",
					env,
				),
				Hint: Localize(
					"export the named env var, or clear passphrase_env for unencrypted archives",
					"指定 env を export するか、非暗号化なら passphrase_env を空にしてください",
				),
			}
		}
	}

	status, err := readArchiveAutoStatus(dbPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doctorCheck{
				Name:   name,
				Status: doctorStatusPass,
				Message: localizef(
					"archive_then_gc enabled (interval=%s); no automatic run recorded yet — next eligible on opportunistic hook maintenance",
					"archive_then_gc 有効 (interval=%s); 自動実行記録はまだありません — 次の hook maintenance で候補",
					interval,
				),
			}
		}
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("failed to read archive auto status: %v", "archive auto status の読み取りに失敗: %v", err),
		}
	}

	if !status.OK {
		return doctorCheck{
			Name:   name,
			Status: doctorStatusWarn,
			Message: localizef(
				"last automatic archive-then-gc failed at %s: %s (rows planned/seen=%d)",
				"直近の automatic archive-then-gc が %s に失敗: %s (rows=%d)",
				status.At, status.Error, status.Rows,
			),
			Hint: Localize(
				"fix config/passphrase_env, free disk, then wait for the next interval or run store archive create manually",
				"config / passphrase_env / ディスクを直し、次 interval を待つか store archive create を手動実行",
			),
			FixCommand: "traceary store archive create --output ~/.config/traceary/archives/manual.trcaryar --delete-after-verify",
		}
	}

	next := ""
	if at, parseErr := time.Parse(time.RFC3339Nano, status.At); parseErr == nil {
		next = at.Add(interval).UTC().Format(time.RFC3339)
	}
	return doctorCheck{
		Name:   name,
		Status: doctorStatusPass,
		Message: localizef(
			"last automatic archive-then-gc OK at %s: rows=%d deleted=%d path=%s next_eligible≈%s",
			"直近 automatic archive-then-gc OK (%s): rows=%d deleted=%d path=%s next_eligible≈%s",
			status.At, status.Rows, status.Deleted, status.Path, next,
		),
	}
}
