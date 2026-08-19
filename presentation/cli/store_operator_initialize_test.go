package cli

import (
	"context"
	"errors"
	"testing"
	"time"
)

type busyThenOKStore struct {
	calls int
}

func (s *busyThenOKStore) Initialize(context.Context) error {
	s.calls++
	if s.calls < 3 {
		return errors.New("failed to run SQLite migrations: failed to query schema_migrations: database is locked (5) (SQLITE_BUSY)")
	}
	return nil
}

func TestInitializeOperatorStoreRetriesBusyUntilSuccess(t *testing.T) {
	t.Parallel()
	store := &busyThenOKStore{}
	if err := initializeOperatorStore(context.Background(), store); err != nil {
		t.Fatalf("initializeOperatorStore() error = %v", err)
	}
	if store.calls != 3 {
		t.Fatalf("Initialize calls = %d, want 3", store.calls)
	}
}

func TestInitializeOperatorStoreDoesNotRetryNonBusy(t *testing.T) {
	t.Parallel()
	store := initializeOnceStore{err: errors.New("schema_migrations records version 1 as x")}
	if err := initializeOperatorStore(context.Background(), store); err == nil {
		t.Fatal("initializeOperatorStore() error = nil, want non-busy failure")
	}
}

func TestInitializeOperatorStoreHonorsContextDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := initializeOperatorStore(ctx, busyForeverStore{})
	if err == nil {
		t.Fatal("initializeOperatorStore() error = nil, want busy failure at deadline")
	}
	if !operatorSQLiteBusy(err) {
		t.Fatalf("error = %v, want SQLITE_BUSY", err)
	}
}

type initializeOnceStore struct{ err error }

func (s initializeOnceStore) Initialize(context.Context) error { return s.err }

type busyForeverStore struct{}

func (busyForeverStore) Initialize(context.Context) error {
	return errors.New("database is locked (5) (SQLITE_BUSY)")
}
