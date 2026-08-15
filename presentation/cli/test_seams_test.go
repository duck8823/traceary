package cli

import (
	"sync"
	"testing"
)

func TestExportTestSeams_ConcurrentHomeDirReadDuringReplace(t *testing.T) {
	homeA := t.TempDir()
	homeB := t.TempDir()
	t.Setenv(dbPathEnvKey, "")
	orig := currentUserHomeDirFunc()
	t.Cleanup(func() { storeUserHomeDirFunc(orig) })

	doneWrite := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-doneWrite:
				return
			default:
				_, _ = resolveDBPath("")
				_, _ = userHomeDirFunc()
			}
		}
	}()
	go func() {
		defer wg.Done()
		defer close(doneWrite)
		for i := 0; i < 2000; i++ {
			storeUserHomeDirFunc(func() (string, error) { return homeA, nil })
			storeUserHomeDirFunc(func() (string, error) { return homeB, nil })
		}
		storeUserHomeDirFunc(orig)
	}()
	wg.Wait()
}
