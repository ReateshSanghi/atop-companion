package storage

import (
	"sort"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

// storedKeys returns every key currently in the metrics bucket, sorted
func storedKeys(t *testing.T) []string {
	t.Helper()
	var keys []string
	err := bboltInstance.View(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(BucketName)).ForEach(func(k, _ []byte) error {
			keys = append(keys, string(k))
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(keys)
	return keys
}

func saveAt(t *testing.T, ts time.Time) string {
	t.Helper()
	stamp := ts.UTC().Format(TimestampLayout)
	if err := SaveMetricSnapshot(HistoricalSnapshot{Timestamp: stamp, CPUUtil: 1, MemUtil: 2, DiskIO: 3}); err != nil {
		t.Fatal(err)
	}
	return "metric:" + stamp
}

func TestPruneOldLogsRemovesOnlyExpiredSamples(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := InitializeStorage(); err != nil {
		t.Fatal(err)
	}
	defer CloseStorage()

	now := time.Now().UTC()
	expired := saveAt(t, now.AddDate(0, 0, -RetentionDays).Add(-time.Hour)) // just outside retention
	saveAt(t, now.AddDate(0, 0, -10))                                       // well outside retention
	kept := saveAt(t, now.AddDate(0, 0, -RetentionDays+1))                  // just inside retention
	fresh := saveAt(t, now)

	if err := PruneOldLogs(); err != nil {
		t.Fatal(err)
	}

	got := storedKeys(t)
	want := []string{kept, fresh}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("after prune: got %v, want %v (expired key was %s)", got, want, expired)
	}
}

func TestPruneOldLogsRemovesManyExpiredSamples(t *testing.T) {
	// Deleting while walking the cursor can skip entries; a batch of expired samples must all go
	t.Chdir(t.TempDir())
	if err := InitializeStorage(); err != nil {
		t.Fatal(err)
	}
	defer CloseStorage()

	now := time.Now().UTC()
	for i := 0; i < 200; i++ {
		saveAt(t, now.AddDate(0, 0, -30).Add(time.Duration(i)*time.Minute))
	}
	fresh := saveAt(t, now)

	if err := PruneOldLogs(); err != nil {
		t.Fatal(err)
	}

	got := storedKeys(t)
	if len(got) != 1 || got[0] != fresh {
		t.Fatalf("expected only the fresh sample to remain, got %d keys: %v", len(got), got)
	}
}
