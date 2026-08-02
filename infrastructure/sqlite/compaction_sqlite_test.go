package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteCompactionBuilderBuildAndVerifyPair(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "source.db")
	candidate := filepath.Join(filepath.Dir(source), "candidate.db")
	db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sample(id TEXT PRIMARY KEY, body BLOB); INSERT INTO sample VALUES('a',x'0001'),('b','text')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	builder := SQLiteCompactionBuilder{}
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.Build(ctx, source, candidate); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("Build mutated source bytes")
	}
	if err := builder.VerifyPair(ctx, source, candidate); err != nil {
		t.Fatal(err)
	}
	candidateDB, err := sql.Open("sqlite", directSQLiteRWDSN(candidate))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidateDB.Exec(`UPDATE sample SET body='changed' WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	if err := candidateDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := builder.VerifyPair(ctx, source, candidate); err == nil {
		t.Fatal("VerifyPair accepted logical mismatch")
	}
}

func directSQLiteRWDSNCreate(path string) string { return "file:" + path }
