package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

type sqliteSerializer interface {
	Serialize() ([]byte, error)
	Deserialize([]byte) error
}

func openSerializedSQLite(ctx context.Context, initial []byte) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file::memory:?cache=private")
	if err != nil {
		return nil, fmt.Errorf("open in-memory candidate sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if len(initial) == 0 {
		return db, nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("acquire candidate deserialize connection: %w", err)
	}
	err = conn.Raw(func(driverConn any) error {
		serializer, ok := driverConn.(sqliteSerializer)
		if !ok {
			return fmt.Errorf("sqlite driver does not support deserialize")
		}
		if deserializeErr := serializer.Deserialize(initial); deserializeErr != nil {
			return fmt.Errorf("deserialize candidate sqlite: %w", deserializeErr)
		}
		return nil
	})
	_ = conn.Close()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run candidate deserialize: %w", err)
	}
	return db, nil
}

func serializeSQLite(ctx context.Context, db *sql.DB) ([]byte, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire candidate serialize connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var serialized []byte
	err = conn.Raw(func(driverConn any) error {
		serializer, ok := driverConn.(sqliteSerializer)
		if !ok {
			return fmt.Errorf("sqlite driver does not support serialize")
		}
		var serializeErr error
		serialized, serializeErr = serializer.Serialize()
		if serializeErr != nil {
			return fmt.Errorf("serialize candidate sqlite: %w", serializeErr)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("run candidate serialize: %w", err)
	}
	return serialized, nil
}
