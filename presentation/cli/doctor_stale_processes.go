package cli

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/xerrors"
)

const (
	doctorStaleProcessesCheckName = "stale-processes"
	doctorStaleProcessScanLimit   = 4096
	doctorStaleProcessReportLimit = 8
	doctorStaleProcessListTimeout = 2 * time.Second
	doctorRetiredMCPServerCommand = "mcp-server"
	linuxProcClockTicksPerSecond  = 100
	linuxProcStatStartTimeField   = 20
)

var cellarTracearyVersionPattern = regexp.MustCompile(`(?i)[/\\]Cellar[/\\]traceary[/\\]([^/\\]+)[/\\]`)

type tracearyProcessSnapshot struct {
	PID        int
	Executable string
	Args       []string
	StartedAt  time.Time
	// Unlinked is true when the kernel still runs an inode that is no
	// longer at Executable (Linux `/proc/<pid>/exe` ends with
	// " (deleted)"). The path then names a replacement file — version
	// and same-file checks against it would inspect the new binary.
	Unlinked bool
}

type staleTracearyProcessFinding struct {
	PID     int
	Version string
	Age     string
	Reason  string
	Exe     string
}

func inspectStaleTracearyProcesses(currentVersion string, now time.Time) doctorCheck {
	check := doctorCheck{Name: doctorStaleProcessesCheckName}
	snapshots, err := listTracearyProcessSnapshotsFunc()
	if err != nil {
		check.Status = doctorStatusWarn
		check.Message = localizef("failed to enumerate Traceary processes: %v", "Traceary プロセスの列挙に失敗しました: %v", err)
		check.Hint = Localize("rerun doctor after confirming `ps` (macOS) or /proc (Linux) is readable", "macOS では `ps`、Linux では /proc が読めることを確認して doctor を再実行してください")
		return check
	}
	findings := classifyStaleTracearyProcesses(snapshots, currentVersion, now)
	if len(findings) == 0 {
		check.Status = doctorStatusPass
		check.Message = Localize("no stale Traceary binaries or retired mcp-server processes are running", "古い Traceary バイナリや引退した mcp-server プロセスは実行されていません")
		return check
	}
	shown := findings
	extra := 0
	if len(shown) > doctorStaleProcessReportLimit {
		extra = len(shown) - doctorStaleProcessReportLimit
		shown = shown[:doctorStaleProcessReportLimit]
	}
	parts := make([]string, 0, len(shown))
	pids := make([]string, 0, len(shown))
	for _, finding := range shown {
		parts = append(parts, fmt.Sprintf("pid=%d version=%s age=%s reason=%s", finding.PID, finding.Version, finding.Age, finding.Reason))
		pids = append(pids, strconv.Itoa(finding.PID))
	}
	message := localizef(
		"stale Traceary processes: %s",
		"古い Traceary プロセスがあります: %s",
		strings.Join(parts, "; "),
	)
	if extra > 0 {
		message += localizef(" (and %d more)", "（ほか %d 件）", extra)
	}
	check.Status = doctorStatusWarn
	check.Message = message
	check.Hint = Localize(
		"quit the host session that launched these processes so they cannot write without a store lease; confirm with `ps -p <pid>` then `kill <pid>` only if unused. MCP (`mcp-server`) was retired in v0.35.0 — remove leftover host config (see docs/mcp/README.md)",
		"store lease なしで書き込ませないよう、これらのプロセスを起動したホスト session を終了してください。`ps -p <pid>` で確認し、未使用なら `kill <pid>`。MCP（`mcp-server`）は v0.35.0 で引退しています。残っている host config を削除してください（docs/mcp/README.ja.md）",
	)
	check.FixCommand = "ps -p " + strings.Join(pids, ",")
	return check
}

func classifyStaleTracearyProcesses(snapshots []tracearyProcessSnapshot, currentVersion string, now time.Time) []staleTracearyProcessFinding {
	selfPID := os.Getpid()
	currentExe, _ := osExecutableFunc()
	current := normalizeDoctorVersion(currentVersion)
	findings := make([]staleTracearyProcessFinding, 0)
	for _, snapshot := range snapshots {
		if snapshot.PID <= 0 || snapshot.PID == selfPID {
			continue
		}
		if !snapshotLooksLikeTraceary(snapshot) {
			continue
		}
		exe := strings.TrimSpace(snapshot.Executable)
		if exe == "" && len(snapshot.Args) > 0 {
			exe = snapshot.Args[0]
		}
		retired := processInvokesRetiredMCPServer(snapshot.Args)
		sameRunningBinary := !snapshot.Unlinked && currentExe != "" && exe != "" && sameFile(exe, currentExe)
		version := inspectTracearyProcessVersion(exe, currentVersion, sameRunningBinary)
		if snapshot.Unlinked {
			version = ""
		}
		if !retired && (sameRunningBinary || versionsMatch(version, current)) {
			continue
		}
		reason := "stale-binary"
		if retired {
			reason = "retired-mcp-server"
		}
		reportedVersion := version
		if reportedVersion == "" {
			reportedVersion = "unknown"
		}
		findings = append(findings, staleTracearyProcessFinding{
			PID:     snapshot.PID,
			Version: reportedVersion,
			Age:     formatProcessAge(snapshot.StartedAt, now),
			Reason:  reason,
			Exe:     exe,
		})
		if len(findings) >= doctorStaleProcessScanLimit {
			break
		}
	}
	return findings
}

func snapshotLooksLikeTraceary(snapshot tracearyProcessSnapshot) bool {
	if looksLikeTracearyExecutable(snapshot.Executable) {
		return true
	}
	if len(snapshot.Args) > 0 && looksLikeTracearyExecutable(snapshot.Args[0]) {
		return true
	}
	return processInvokesRetiredMCPServer(snapshot.Args)
}

func looksLikeTracearyExecutable(path string) bool {
	base := filepath.Base(strings.TrimSpace(path))
	base = strings.TrimSuffix(base, ".exe")
	if idx := strings.Index(base, " (deleted)"); idx >= 0 {
		base = strings.TrimSpace(base[:idx])
	}
	return base == "traceary"
}

func processInvokesRetiredMCPServer(args []string) bool {
	for _, arg := range args {
		if arg == doctorRetiredMCPServerCommand {
			return true
		}
	}
	return false
}

func versionsMatch(processVersion, currentVersion string) bool {
	processVersion = normalizeDoctorVersion(processVersion)
	currentVersion = normalizeDoctorVersion(currentVersion)
	if processVersion == "" || currentVersion == "" {
		return false
	}
	if isDevBuild(processVersion) || isDevBuild(currentVersion) {
		return processVersion == currentVersion
	}
	return processVersion == currentVersion
}

func inspectTracearyProcessVersion(exe, currentVersion string, sameRunningBinary bool) string {
	if sameRunningBinary {
		return normalizeDoctorVersion(currentVersion)
	}
	if version := cellarTracearyVersion(exe); version != "" {
		return version
	}
	if version := goBuildInfoVersion(exe); version != "" {
		return version
	}
	return ""
}

func cellarTracearyVersion(exe string) string {
	match := cellarTracearyVersionPattern.FindStringSubmatch(exe)
	if len(match) < 2 {
		return ""
	}
	return normalizeDoctorVersion(match[1])
}

func goBuildInfoVersion(exe string) string {
	if strings.TrimSpace(exe) == "" {
		return ""
	}
	info, err := buildinfo.ReadFile(exe)
	if err != nil || info == nil {
		return ""
	}
	version := strings.TrimSpace(info.Main.Version)
	if version == "" || version == "(devel)" {
		return ""
	}
	return normalizeDoctorVersion(version)
}

func formatProcessAge(startedAt, now time.Time) string {
	if startedAt.IsZero() || now.IsZero() {
		return "unknown"
	}
	duration := now.Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	switch {
	case duration >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(duration.Hours()/24))
	case duration >= time.Hour:
		return fmt.Sprintf("%dh", int(duration.Hours()))
	case duration >= time.Minute:
		return fmt.Sprintf("%dm", int(duration.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(duration.Seconds()))
	}
}

func defaultListTracearyProcessSnapshots() ([]tracearyProcessSnapshot, error) {
	switch runtime.GOOS {
	case "darwin":
		return listTracearyProcessSnapshotsDarwin()
	case "linux":
		return listTracearyProcessSnapshotsLinux()
	default:
		return nil, nil
	}
}

func listTracearyProcessSnapshotsDarwin() ([]tracearyProcessSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), doctorStaleProcessListTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,etime=,args=").Output()
	if err != nil {
		return nil, xerrors.Errorf("failed to list processes with ps: %w", err)
	}
	return parsePSTracearyProcessSnapshots(string(output), time.Now()), nil
}

func parsePSTracearyProcessSnapshots(output string, now time.Time) []tracearyProcessSnapshot {
	lines := strings.Split(output, "\n")
	snapshots := make([]tracearyProcessSnapshot, 0)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		args := fields[2:]
		exe := args[0]
		if !looksLikeTracearyExecutable(exe) && !processInvokesRetiredMCPServer(args) {
			continue
		}
		snapshots = append(snapshots, tracearyProcessSnapshot{
			PID:        pid,
			Executable: exe,
			Args:       args,
			StartedAt:  now.Add(-parsePSElapsed(fields[1])),
		})
		if len(snapshots) >= doctorStaleProcessScanLimit {
			break
		}
	}
	return snapshots
}

func parsePSElapsed(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	days := 0
	rest := value
	if dayPart, after, ok := strings.Cut(value, "-"); ok {
		parsedDays, err := strconv.Atoi(dayPart)
		if err != nil {
			return 0
		}
		days = parsedDays
		rest = after
	}
	parts := strings.Split(rest, ":")
	var hours, minutes, seconds int
	var err error
	switch len(parts) {
	case 1:
		seconds, err = strconv.Atoi(parts[0])
	case 2:
		minutes, err = strconv.Atoi(parts[0])
		if err == nil {
			seconds, err = strconv.Atoi(parts[1])
		}
	case 3:
		hours, err = strconv.Atoi(parts[0])
		if err == nil {
			minutes, err = strconv.Atoi(parts[1])
		}
		if err == nil {
			seconds, err = strconv.Atoi(parts[2])
		}
	default:
		return 0
	}
	if err != nil {
		return 0
	}
	return time.Duration(days)*24*time.Hour +
		time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds)*time.Second
}

func listTracearyProcessSnapshotsLinux() ([]tracearyProcessSnapshot, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, xerrors.Errorf("failed to read /proc: %w", err)
	}
	now := time.Now()
	boot, bootErr := linuxBootTime(now)
	snapshots := make([]tracearyProcessSnapshot, 0)
	scanned := 0
	for _, entry := range entries {
		if scanned >= doctorStaleProcessScanLimit {
			break
		}
		name := entry.Name()
		pid, convErr := strconv.Atoi(name)
		if convErr != nil || pid <= 0 {
			continue
		}
		scanned++
		args, argsErr := readProcCmdline(pid)
		if argsErr != nil || len(args) == 0 {
			continue
		}
		exe, unlinked := linuxProcExecutable(pid, args[0])
		if !looksLikeTracearyExecutable(exe) && !looksLikeTracearyExecutable(args[0]) && !processInvokesRetiredMCPServer(args) {
			continue
		}
		startedAt := time.Time{}
		if bootErr == nil {
			if start, startErr := linuxProcStartTime(pid, boot); startErr == nil {
				startedAt = start
			}
		}
		snapshots = append(snapshots, tracearyProcessSnapshot{
			PID:        pid,
			Executable: exe,
			Args:       args,
			StartedAt:  startedAt,
			Unlinked:   unlinked,
		})
	}
	return snapshots, nil
}

func readProcCmdline(pid int) ([]string, error) {
	payload, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, xerrors.Errorf("failed to read cmdline for pid %d: %w", pid, err)
	}
	if len(payload) == 0 {
		return nil, nil
	}
	parts := bytes.Split(payload, []byte{0})
	args := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		args = append(args, string(part))
	}
	return args, nil
}

func linuxProcExecutable(pid int, argv0 string) (string, bool) {
	target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return argv0, false
	}
	unlinked := strings.HasSuffix(target, " (deleted)")
	target = strings.TrimSuffix(target, " (deleted)")
	return target, unlinked
}

func linuxBootTime(now time.Time) (time.Time, error) {
	payload, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return time.Time{}, xerrors.Errorf("failed to read /proc/uptime: %w", err)
	}
	fields := strings.Fields(string(payload))
	if len(fields) == 0 {
		return time.Time{}, xerrors.New("empty /proc/uptime")
	}
	uptime, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return time.Time{}, xerrors.Errorf("failed to parse /proc/uptime: %w", err)
	}
	return now.Add(-time.Duration(uptime * float64(time.Second))), nil
}

func linuxProcStartTime(pid int, boot time.Time) (time.Time, error) {
	payload, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return time.Time{}, xerrors.Errorf("failed to read stat for pid %d: %w", pid, err)
	}
	rest := payload
	idx := bytes.LastIndex(payload, []byte(")"))
	if idx >= 0 && idx+2 <= len(payload) {
		rest = payload[idx+2:]
	}
	fields := strings.Fields(string(rest))
	if len(fields) < linuxProcStatStartTimeField {
		return time.Time{}, xerrors.Errorf("stat for pid %d is truncated", pid)
	}
	ticks, err := strconv.ParseUint(fields[linuxProcStatStartTimeField-1], 10, 64)
	if err != nil {
		return time.Time{}, xerrors.Errorf("failed to parse starttime for pid %d: %w", pid, err)
	}
	return boot.Add(time.Duration(ticks) * time.Second / linuxProcClockTicksPerSecond), nil
}
