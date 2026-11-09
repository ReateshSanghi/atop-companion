package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"go.etcd.io/bbolt"
)

// HistoricalSnapshot replicates our shared telemetry model framework natively
type HistoricalSnapshot struct {
	Timestamp string  `json:"timestamp"` // Format: YYYY-MM-DD HH:mm:ss
	CPUUtil   float64 `json:"cpu"`
	MemUtil   float64 `json:"mem"`
	DiskIO    float64 `json:"disk_io"`
}

var bboltInstance *bbolt.DB

const BucketName = "AtopHistoryBucket"
const DbFileName = "atop_historical_data.db"

// RetentionDays is how long samples are kept before PruneOldLogs removes them
const RetentionDays = 7

// TimestampLayout is the format of the Timestamp field and the suffix of every stored key
const TimestampLayout = "2006-01-02 15:04:05"

// InitializeStorage Engine mounts the single flat-file DB and establishes indices
func InitializeStorage() error {
	var err error
	bboltInstance, err = bbolt.Open(DbFileName, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return fmt.Errorf("failed to open embedded database: %w", err)
	}

	// Create our top-level keyspace bucket natively
	return bboltInstance.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(BucketName))
		return err
	})
}

// CloseStorage securely flushes any pending IO memory map streams to disk
func CloseStorage() {
	if bboltInstance != nil {
		bboltInstance.Close()
		bboltInstance = nil
	}
}

// SaveMetricSnapshot serializes and logs a parsed chronological timeline record
func SaveMetricSnapshot(snapshot HistoricalSnapshot) error {
	if bboltInstance == nil {
		return fmt.Errorf("database storage engine is offline")
	}

	return bboltInstance.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketName))

		// Index structural keys by timestamp for fast sequential sorting
		key := fmt.Sprintf("metric:%s", snapshot.Timestamp)
		dataBytes, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}

		return bucket.Put([]byte(key), dataBytes)
	})
}

// SaveMetricSnapshots stores many samples in a single transaction. One transaction per sample forces a disk
// sync each time, which made importing a log file slow.
func SaveMetricSnapshots(snapshots []HistoricalSnapshot) error {
	if bboltInstance == nil {
		return fmt.Errorf("database storage engine is offline")
	}

	return bboltInstance.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketName))
		for _, snapshot := range snapshots {
			dataBytes, err := json.Marshal(snapshot)
			if err != nil {
				return err
			}
			if err := bucket.Put([]byte("metric:"+snapshot.Timestamp), dataBytes); err != nil {
				return err
			}
		}
		return nil
	})
}

// PruneOldLogs checks and wipes data older than RetentionDays from the archive
func PruneOldLogs() error {
	if bboltInstance == nil {
		return nil
	}

	// Keys are "metric:" + UTC timestamp in TimestampLayout, so the cutoff must use the same layout and zone
	cutoffKey := "metric:" + time.Now().UTC().AddDate(0, 0, -RetentionDays).Format(TimestampLayout)

	return bboltInstance.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketName))

		// Collect expired keys first; deleting while the cursor is walking can skip entries
		var expired [][]byte
		cursor := bucket.Cursor()
		for key, _ := cursor.First(); key != nil && string(key) < cutoffKey; key, _ = cursor.Next() {
			expired = append(expired, append([]byte(nil), key...))
		}

		for _, key := range expired {
			if err := bucket.Delete(key); err != nil {
				return err
			}
		}
		return nil
	})
}

// QueryTimeframeMetrics returns chronological entries matching specific time windows
func QueryTimeframeMetrics(targetTimeSuffix string) ([]HistoricalSnapshot, error) {
	var results []HistoricalSnapshot
	if bboltInstance == nil {
		return results, fmt.Errorf("database storage offline")
	}

	err := bboltInstance.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketName))
		return bucket.ForEach(func(k, v []byte) error {
			var snp HistoricalSnapshot
			if err := json.Unmarshal(v, &snp); err == nil {
				// Match specific segments across days (e.g. check index space matching "14:00:")
				if len(snp.Timestamp) > 16 && snp.Timestamp[11:17] == targetTimeSuffix {
					results = append(results, snp)
				}
			}
			return nil
		})
	})

	return results, err
}

// ClearAllDatabaseLogs wipes out the database file space cleanly
func ClearAllDatabaseLogs() error {
	CloseStorage()
	_ = os.Remove(DbFileName)
	return InitializeStorage()
}
