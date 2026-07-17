package usecase

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"time"

	"golang.org/x/xerrors"
)

const (
	storeArchiveMagic   = "TRCARYAR"
	storeArchiveVersion = byte(1)
	storeArchiveFormat  = "traceary.store.archive"
)

// storeArchiveManifest is the v1 on-disk manifest (see docs/storage/archive-manifest.v1.schema.json).
type storeArchiveManifest struct {
	SchemaVersion int    `json:"schema_version"`
	Format        string `json:"format"`
	CreatedAt     string `json:"created_at"`
	ToolVersion   string `json:"tool_version"`
	SourceDB      *storeArchiveSourceFingerprint `json:"source_db_fingerprint,omitempty"`
	Plan          storeArchivePlan               `json:"plan"`
	Tables        []storeArchiveTableMeta        `json:"tables"`
	Totals        storeArchiveTotals             `json:"totals"`
	PayloadSHA256 string                         `json:"payload_sha256"`
	Encryption    storeArchiveEncryption         `json:"encryption"`
}

type storeArchiveSourceFingerprint struct {
	Path              string `json:"path"`
	PageCount         int    `json:"page_count,omitempty"`
	SchemaUserVersion int    `json:"schema_user_version,omitempty"`
}

type storeArchivePlan struct {
	Target   string `json:"target"`
	KeepDays int    `json:"keep_days"`
	Cutoff   string `json:"cutoff"`
	DryRun   bool   `json:"dry_run"`
}

type storeArchiveTableMeta struct {
	Name               string   `json:"name"`
	PrimaryKey         []string `json:"primary_key"`
	RowCount           int      `json:"row_count"`
	NDJSONSHA256       string   `json:"ndjson_sha256"`
	RowIDsSHA256       string   `json:"row_ids_sha256"`
	CompressedBytes    int      `json:"compressed_bytes"`
	UncompressedBytes  int      `json:"uncompressed_bytes"`
}

type storeArchiveTotals struct {
	Rows              int `json:"rows"`
	CompressedBytes   int `json:"compressed_bytes"`
	UncompressedBytes int `json:"uncompressed_bytes"`
}

type storeArchiveEncryption struct {
	Mode string `json:"mode"`
}

// storeArchiveTableData is in-memory table payload before packaging.
type storeArchiveTableData struct {
	Name       string
	PrimaryKey []string
	// Rows are JSON objects; IDs extracted via PrimaryKey fields in order.
	Rows []map[string]any
}

func buildStoreArchivePackage(tables []storeArchiveTableData, plan storeArchivePlan, toolVersion, sourcePath string, passphrase []byte) ([]byte, storeArchiveManifest, error) {
	sort.SliceStable(tables, func(i, j int) bool {
		return archiveTableOrder(tables[i].Name) < archiveTableOrder(tables[j].Name)
	})

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	metaTables := make([]storeArchiveTableMeta, 0, len(tables))
	totalRows := 0
	totalUncompressed := 0

	for _, table := range tables {
		ndjson, ids, err := encodeArchiveNDJSON(table)
		if err != nil {
			return nil, storeArchiveManifest{}, err
		}
		ndHash := sha256Hex(ndjson)
		idHash := sha256Hex([]byte(strings.Join(ids, "\n") + "\n"))
		if err := writeTarFile(tw, "tables/"+table.Name+".ndjson", ndjson); err != nil {
			return nil, storeArchiveManifest{}, err
		}
		if err := writeTarFile(tw, "tables/"+table.Name+".sha256", []byte(ndHash+"\n")); err != nil {
			return nil, storeArchiveManifest{}, err
		}
		metaTables = append(metaTables, storeArchiveTableMeta{
			Name:              table.Name,
			PrimaryKey:        append([]string(nil), table.PrimaryKey...),
			RowCount:          len(table.Rows),
			NDJSONSHA256:      ndHash,
			RowIDsSHA256:      idHash,
			CompressedBytes:   0, // filled after gzip for whole payload; per-table left 0 in v1
			UncompressedBytes: len(ndjson),
		})
		totalRows += len(table.Rows)
		totalUncompressed += len(ndjson)
	}

	// Placeholder manifest for payload hash of table members only: we hash the
	// gzip payload after writing tables, then rewrite is not possible — so
	// payload_sha256 is the sha256 of the concatenated table digests line list.
	payloadDigestInput := &bytes.Buffer{}
	for _, m := range metaTables {
		_, _ = payloadDigestInput.WriteString(m.Name + ":" + m.NDJSONSHA256 + "\n")
	}
	payloadSHA := sha256Hex(payloadDigestInput.Bytes())

	encMode := "none"
	if len(passphrase) > 0 {
		encMode = "xchacha20poly1305-argon2id"
	}
	manifest := storeArchiveManifest{
		SchemaVersion: 1,
		Format:        storeArchiveFormat,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		ToolVersion:   toolVersion,
		Plan:          plan,
		Tables:        metaTables,
		Totals: storeArchiveTotals{
			Rows:              totalRows,
			UncompressedBytes: totalUncompressed,
		},
		PayloadSHA256: payloadSHA,
		Encryption: storeArchiveEncryption{
			Mode: encMode,
		},
	}
	if strings.TrimSpace(sourcePath) != "" {
		manifest.SourceDB = &storeArchiveSourceFingerprint{Path: sourcePath}
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, storeArchiveManifest{}, xerrors.Errorf("marshal manifest: %w", err)
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := writeTarFile(tw, "manifest.json", manifestBytes); err != nil {
		return nil, storeArchiveManifest{}, err
	}
	if err := tw.Close(); err != nil {
		return nil, storeArchiveManifest{}, xerrors.Errorf("tar close: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, storeArchiveManifest{}, xerrors.Errorf("gzip close: %w", err)
	}

	payload := buf.Bytes()
	manifest.Totals.CompressedBytes = len(payload)

	outer := &bytes.Buffer{}
	outer.WriteString(storeArchiveMagic)
	outer.WriteByte(storeArchiveVersion)
	if len(passphrase) > 0 {
		sealed, err := sealBundle(payload, passphrase)
		if err != nil {
			return nil, storeArchiveManifest{}, xerrors.Errorf("seal archive: %w", err)
		}
		outer.Write(sealed)
		return outer.Bytes(), manifest, nil
	}
	outer.Write(payload)
	return outer.Bytes(), manifest, nil
}

func openStoreArchivePackage(data, passphrase []byte) (storeArchiveManifest, map[string][]byte, error) {
	if len(data) < len(storeArchiveMagic)+1 {
		return storeArchiveManifest{}, nil, xerrors.Errorf("archive is too short")
	}
	if string(data[:len(storeArchiveMagic)]) != storeArchiveMagic {
		return storeArchiveManifest{}, nil, xerrors.Errorf("archive does not have the Traceary archive magic prefix")
	}
	version := data[len(storeArchiveMagic)]
	if version != storeArchiveVersion {
		return storeArchiveManifest{}, nil, xerrors.Errorf("unsupported archive version %d", version)
	}
	body := data[len(storeArchiveMagic)+1:]
	// Detect nested sealed bundle envelope (bundle magic TRBUNDLE) vs raw gzip.
	if len(body) >= len(bundleMagic) && bytes.Equal(body[:len(bundleMagic)], bundleMagic) {
		if len(passphrase) == 0 {
			return storeArchiveManifest{}, nil, xerrors.Errorf("archive is encrypted; provide passphrase")
		}
		plain, err := openBundle(body, passphrase)
		if err != nil {
			return storeArchiveManifest{}, nil, xerrors.Errorf("decrypt archive: %w", err)
		}
		body = plain
	}
	files, err := untarGzip(body)
	if err != nil {
		return storeArchiveManifest{}, nil, err
	}
	manifestRaw, ok := files["manifest.json"]
	if !ok {
		return storeArchiveManifest{}, nil, xerrors.Errorf("archive missing manifest.json")
	}
	var manifest storeArchiveManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return storeArchiveManifest{}, nil, xerrors.Errorf("parse manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 || manifest.Format != storeArchiveFormat {
		return storeArchiveManifest{}, nil, xerrors.Errorf("unsupported archive manifest schema/format")
	}
	return manifest, files, nil
}

func verifyStoreArchiveContents(manifest storeArchiveManifest, files map[string][]byte) error {
	payloadDigestInput := &bytes.Buffer{}
	for _, table := range manifest.Tables {
		ndjson, ok := files["tables/"+table.Name+".ndjson"]
		if !ok {
			return xerrors.Errorf("missing ndjson for table %s", table.Name)
		}
		if sha256Hex(ndjson) != table.NDJSONSHA256 {
			return xerrors.Errorf("ndjson digest mismatch for table %s", table.Name)
		}
		if side, ok := files["tables/"+table.Name+".sha256"]; ok {
			if strings.TrimSpace(string(side)) != table.NDJSONSHA256 {
				return xerrors.Errorf("side-car sha256 mismatch for table %s", table.Name)
			}
		}
		ids, err := extractIDsFromNDJSON(ndjson, table.PrimaryKey)
		if err != nil {
			return xerrors.Errorf("parse ndjson %s: %w", table.Name, err)
		}
		if len(ids) != table.RowCount {
			return xerrors.Errorf("row_count mismatch for table %s: manifest=%d actual=%d", table.Name, table.RowCount, len(ids))
		}
		idHash := sha256Hex([]byte(strings.Join(ids, "\n") + "\n"))
		if idHash != table.RowIDsSHA256 {
			return xerrors.Errorf("row_ids_sha256 mismatch for table %s", table.Name)
		}
		_, _ = payloadDigestInput.WriteString(table.Name + ":" + table.NDJSONSHA256 + "\n")
	}
	if sha256Hex(payloadDigestInput.Bytes()) != manifest.PayloadSHA256 {
		return xerrors.Errorf("payload_sha256 mismatch")
	}
	return nil
}

func encodeArchiveNDJSON(table storeArchiveTableData) ([]byte, []string, error) {
	ids := make([]string, 0, len(table.Rows))
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Stable order by composite PK string.
	sort.SliceStable(table.Rows, func(i, j int) bool {
		return compositeID(table.Rows[i], table.PrimaryKey) < compositeID(table.Rows[j], table.PrimaryKey)
	})
	for _, row := range table.Rows {
		id := compositeID(row, table.PrimaryKey)
		if id == "" {
			return nil, nil, xerrors.Errorf("table %s row missing primary key fields", table.Name)
		}
		ids = append(ids, id)
		if err := enc.Encode(row); err != nil {
			return nil, nil, xerrors.Errorf("encode row: %w", err)
		}
	}
	return buf.Bytes(), ids, nil
}

func extractIDsFromNDJSON(ndjson []byte, primaryKey []string) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(ndjson))
	var ids []string
	for {
		var row map[string]any
		if err := dec.Decode(&row); err != nil {
			if err == io.EOF {
				break
			}
			return nil, xerrors.Errorf("decode ndjson row: %w", err)
		}
		ids = append(ids, compositeID(row, primaryKey))
	}
	return ids, nil
}

func compositeID(row map[string]any, primaryKey []string) string {
	parts := make([]string, 0, len(primaryKey))
	for _, k := range primaryKey {
		v, ok := row[k]
		if !ok || v == nil {
			return ""
		}
		parts = append(parts, stringifyJSONScalar(v))
	}
	return strings.Join(parts, "\x00")
}

func stringifyJSONScalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		// JSON numbers
		if t == float64(int64(t)) {
			return jsonNumberInt(int64(t))
		}
		b, _ := json.Marshal(t)
		return string(b)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func jsonNumberInt(n int64) string {
	return strings.TrimSpace(string(mustJSON(n)))
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}

func writeTarFile(tw *tar.Writer, name string, content []byte) error {
	hdr := &tar.Header{
		Name: name,
		Mode: 0o600,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return xerrors.Errorf("tar header %s: %w", name, err)
	}
	if _, err := tw.Write(content); err != nil {
		return xerrors.Errorf("tar write %s: %w", name, err)
	}
	return nil
}

func untarGzip(data []byte) (map[string][]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, xerrors.Errorf("gzip open: %w", err)
	}
	defer func() { _ = gr.Close() }()
	tr := tar.NewReader(gr)
	out := make(map[string][]byte)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, xerrors.Errorf("tar next: %w", err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			return nil, xerrors.Errorf("tar read %s: %w", hdr.Name, err)
		}
		out[hdr.Name] = content
	}
	return out, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func archiveTableOrder(name string) int {
	order := map[string]int{
		"events":                10,
		"command_audits":        20,
		"sessions":              30,
		"memories":              40,
		"memory_evidence_refs":  50,
		"memory_artifact_refs":  60,
		"memory_edges":          70,
	}
	if n, ok := order[name]; ok {
		return n
	}
	return 1000
}

// ParseStoreArchiveTables returns table name → rows for restore.
func parseStoreArchiveTables(manifest storeArchiveManifest, files map[string][]byte) (map[string][]map[string]any, error) {
	out := make(map[string][]map[string]any, len(manifest.Tables))
	for _, table := range manifest.Tables {
		ndjson, ok := files["tables/"+table.Name+".ndjson"]
		if !ok {
			return nil, xerrors.Errorf("missing ndjson for %s", table.Name)
		}
		dec := json.NewDecoder(bytes.NewReader(ndjson))
		var rows []map[string]any
		for {
			var row map[string]any
			if err := dec.Decode(&row); err != nil {
				if err == io.EOF {
					break
				}
				return nil, xerrors.Errorf("decode %s: %w", table.Name, err)
			}
			rows = append(rows, row)
		}
		out[table.Name] = rows
	}
	return out, nil
}
