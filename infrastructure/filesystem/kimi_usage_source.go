package filesystem

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

const (
	kimiUsageHomeEnv       = "KIMI_CODE_HOME"
	kimiUsageDefaultHome   = ".kimi-code"
	kimiUsageSessionIndex  = "session_index.jsonl"
	kimiUsageMaxLineBytes  = 8 * 1024 * 1024
	kimiUsageMaxSourceSize = 128 * 1024 * 1024
	kimiUsageSourceVersion = "0.29.0"
	kimiUsageMaxTimestamp  = int64(253402300799999)
)

type kimiUsageSource struct {
	root func() (string, error)
}

// NewKimiUsageSource creates the contained, body-free Kimi wire adapter.
func NewKimiUsageSource() application.KimiUsageSource {
	return &kimiUsageSource{root: defaultKimiUsageRoot}
}

func newKimiUsageSourceWithRoot(root string) *kimiUsageSource {
	return &kimiUsageSource{root: func() (string, error) { return root, nil }}
}

func defaultKimiUsageRoot() (string, error) {
	if configured := strings.TrimSpace(os.Getenv(kimiUsageHomeEnv)); configured != "" {
		return configured, nil
	}
	home, err := osUserHomeDir()
	if err != nil {
		return "", xerrors.Errorf("failed to resolve Kimi usage home")
	}
	return filepath.Join(home, kimiUsageDefaultHome), nil
}

type kimiUsageIndexEntry struct {
	SessionID  string `json:"sessionId"`
	SessionDir string `json:"sessionDir"`
}

type kimiUsageEnvelope struct {
	Type string `json:"type"`
}

type kimiUsageWireRecord struct {
	Type       string `json:"type"`
	Model      string `json:"model"`
	UsageScope string `json:"usageScope"`
	Time       *int64 `json:"time"`
	Usage      *struct {
		InputOther         *int64 `json:"inputOther"`
		InputCacheRead     *int64 `json:"inputCacheRead"`
		InputCacheCreation *int64 `json:"inputCacheCreation"`
		Output             *int64 `json:"output"`
	} `json:"usage"`
}

func (s *kimiUsageSource) Load(
	ctx context.Context,
	providerSessionID string,
) (application.KimiUsageLoadResult, error) {
	if !validKimiUsageIdentity(providerSessionID) {
		return application.KimiUsageLoadResult{}, xerrors.Errorf("invalid Kimi usage session identity")
	}
	if s == nil || s.root == nil {
		return application.KimiUsageLoadResult{}, xerrors.Errorf("Kimi usage source is not configured")
	}
	root, err := s.root()
	if err != nil || strings.TrimSpace(root) == "" {
		return application.KimiUsageLoadResult{}, xerrors.Errorf("failed to resolve Kimi usage home")
	}
	sessionDir, found, err := findKimiUsageSessionDir(ctx, filepath.Join(root, kimiUsageSessionIndex), providerSessionID)
	if err != nil {
		return application.KimiUsageLoadResult{}, err
	}
	if !found {
		return application.KimiUsageLoadResult{}, nil
	}
	wirePath, found, err := containedKimiUsageWire(root, sessionDir)
	if err != nil {
		return application.KimiUsageLoadResult{}, err
	}
	if !found {
		return application.KimiUsageLoadResult{}, nil
	}
	return loadKimiUsageWire(ctx, wirePath, providerSessionID)
}

func findKimiUsageSessionDir(
	ctx context.Context,
	indexPath string,
	providerSessionID string,
) (string, bool, error) {
	file, err := os.Open(indexPath) // #nosec G304 -- fixed name below the configured Kimi home
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, xerrors.Errorf("failed to open Kimi usage session index")
	}
	defer func() { _ = file.Close() }()
	if info, statErr := file.Stat(); statErr != nil || info.Size() > kimiUsageMaxSourceSize {
		return "", false, xerrors.Errorf("Kimi usage session index exceeds the source limit")
	}

	sessionDir := ""
	scanner, limited := boundedKimiUsageScanner(file)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return "", false, xerrors.Errorf("Kimi usage session index scan cancelled: %w", err)
		}
		var entry kimiUsageIndexEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.SessionID == providerSessionID && strings.TrimSpace(entry.SessionDir) != "" {
			sessionDir = entry.SessionDir
		}
	}
	if err := scanner.Err(); err != nil {
		return "", false, xerrors.Errorf("failed to scan Kimi usage session index")
	}
	if limited.N == 0 {
		return "", false, xerrors.Errorf("Kimi usage session index exceeds the source limit")
	}
	return sessionDir, sessionDir != "", nil
}

func containedKimiUsageWire(root, sessionDir string) (string, bool, error) {
	sessionsRoot := filepath.Join(root, "sessions")
	resolvedRoot, err := filepath.EvalSymlinks(sessionsRoot)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, xerrors.Errorf("failed to resolve Kimi usage sessions root")
	}
	candidate := filepath.Join(sessionDir, "agents", "main", "wire.jsonl")
	resolvedCandidate, err := filepath.EvalSymlinks(filepath.Clean(candidate))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, xerrors.Errorf("failed to resolve Kimi usage wire")
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false, xerrors.Errorf("Kimi usage wire escapes the sessions root")
	}
	return resolvedCandidate, true, nil
}

func loadKimiUsageWire(
	ctx context.Context,
	wirePath string,
	providerSessionID string,
) (application.KimiUsageLoadResult, error) {
	file, err := os.Open(wirePath) // #nosec G304 -- path passed the sessions-root containment check
	if os.IsNotExist(err) {
		return application.KimiUsageLoadResult{}, nil
	}
	if err != nil {
		return application.KimiUsageLoadResult{}, xerrors.Errorf("failed to open Kimi usage wire")
	}
	defer func() { _ = file.Close() }()
	if info, statErr := file.Stat(); statErr != nil || info.Size() > kimiUsageMaxSourceSize {
		return application.KimiUsageLoadResult{}, xerrors.Errorf("Kimi usage wire exceeds the source limit")
	}

	result := application.KimiUsageLoadResult{}
	usageOrdinal := int64(0)
	turnOrdinal := int64(0)
	scanner, limited := boundedKimiUsageScanner(file)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return application.KimiUsageLoadResult{}, xerrors.Errorf("Kimi usage wire scan cancelled: %w", err)
		}
		var envelope kimiUsageEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			return application.KimiUsageLoadResult{}, xerrors.Errorf("invalid Kimi usage wire event")
		}
		switch envelope.Type {
		case "turn.prompt":
			turnOrdinal++
		case "usage.record":
			usageOrdinal++
			sample, err := decodeKimiUsageRecord(scanner.Bytes(), providerSessionID, usageOrdinal)
			if err != nil {
				return application.KimiUsageLoadResult{}, err
			}
			result.Samples = append(result.Samples, sample)
		}
	}
	if err := scanner.Err(); err != nil {
		return application.KimiUsageLoadResult{}, xerrors.Errorf("failed to scan Kimi usage wire")
	}
	if limited.N == 0 {
		return application.KimiUsageLoadResult{}, xerrors.Errorf("Kimi usage wire exceeds the source limit")
	}
	result.LatestTurnOrdinal = turnOrdinal
	return result, nil
}

func decodeKimiUsageRecord(
	line []byte,
	providerSessionID string,
	ordinal int64,
) (application.KimiUsageSample, error) {
	var record kimiUsageWireRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return application.KimiUsageSample{}, xerrors.Errorf("invalid Kimi usage record")
	}
	model := strings.TrimSpace(record.Model)
	if !validKimiUsageIdentity(model) || record.UsageScope != "turn" || record.Time == nil ||
		*record.Time < 0 || *record.Time > kimiUsageMaxTimestamp || record.Usage == nil {
		return application.KimiUsageSample{}, xerrors.Errorf("invalid Kimi usage record metadata")
	}
	values := []*int64{
		record.Usage.InputOther,
		record.Usage.InputCacheRead,
		record.Usage.InputCacheCreation,
		record.Usage.Output,
	}
	present := false
	for _, value := range values {
		if value == nil {
			continue
		}
		present = true
		if *value < 0 {
			return application.KimiUsageSample{}, xerrors.Errorf("invalid negative Kimi usage counter")
		}
	}
	if !present {
		return application.KimiUsageSample{}, xerrors.Errorf("empty Kimi usage record")
	}
	digest := sha256.Sum256([]byte(providerSessionID + "\x00main\x00" + strconv.FormatInt(ordinal, 10)))
	return application.KimiUsageSample{
		RecordID:      "main_wire:" + hex.EncodeToString(digest[:]),
		SourceName:    "main_wire",
		SourceVersion: kimiUsageSourceVersion,
		Model:         model,
		ObservedAt:    time.UnixMilli(*record.Time).UTC(),
		Counters: application.KimiUsageCounters{
			InputOther:         record.Usage.InputOther,
			InputCacheRead:     record.Usage.InputCacheRead,
			InputCacheCreation: record.Usage.InputCacheCreation,
			Output:             record.Usage.Output,
		},
	}, nil
}

func boundedKimiUsageScanner(reader io.Reader) (*bufio.Scanner, *io.LimitedReader) {
	limited := &io.LimitedReader{R: reader, N: kimiUsageMaxSourceSize + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 0, 64*1024), kimiUsageMaxLineBytes)
	return scanner, limited
}

func validKimiUsageIdentity(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 512 && !strings.ContainsAny(value, "\r\n\x00")
}
