package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
)

const geodataHashBaselinesKey = "geodataHashBaselines"

type geodataHashBaseline struct {
	Hash        string `json:"hash"`
	ConfirmedAt int64  `json:"confirmedAt"`
	RunID       string `json:"runId"`
}

type geodataHashSample struct {
	file string
	path string
	hash string
	err  error
}

func geodataHashFile(path string, completedAt time.Time) (string, error) {
	file, err := openGeodataHashFile(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", errors.New("Geo hash requires a regular file")
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	after, err := file.Stat()
	if err != nil {
		return "", err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", errors.New("Geo file changed while calculating its hash")
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".*.bak"))
	if err != nil {
		return "", err
	}
	if len(backups) != 0 {
		return "", errors.New("Geo update has not finished committing its files")
	}
	current, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !os.SameFile(after, current) || after.Size() != current.Size() || !after.ModTime().Equal(current.ModTime()) {
		return "", errors.New("Geo file changed while calculating its hash")
	}
	if !completedAt.IsZero() && current.ModTime().After(completedAt.Add(time.Microsecond)) {
		return "", errors.New("geoHashAfterRun")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func geodataHashBaselinePath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func geodataHashBaselinesLocked() (map[string]geodataHashBaseline, error) {
	baselines := make(map[string]geodataHashBaseline)
	stored, err := (&SettingService{}).getSetting(geodataHashBaselinesKey)
	if database.IsNotFound(err) {
		return baselines, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(stored.Value), &baselines); err != nil {
		return nil, err
	}
	if baselines == nil {
		baselines = make(map[string]geodataHashBaseline)
	}
	return baselines, nil
}

// Save the comparison and its baseline together so failed writes cannot advance it.
func saveGeodataHashStateLocked(record TaskExecutionLog, baselines map[string]geodataHashBaseline) error {
	cfg, err := taskLogSettingsLocked()
	if err != nil {
		return err
	}
	history, err := taskHistoryLocked()
	if err != nil {
		return err
	}
	history = prunedTaskHistory(upsertTaskExecutionLog(history, record), cfg)
	encodedBaselines, err := json.Marshal(baselines)
	if err != nil {
		return err
	}
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := writeTaskHistory(tx, history); err != nil {
			return err
		}
		return writeTaskLogSetting(tx, geodataHashBaselinesKey, string(encodedBaselines))
	})
}

func saveGeodataHashes(record *TaskExecutionLog, samples []geodataHashSample, confirmedAt int64, timezone string) (map[string]*geodataHashBaseline, error) {
	taskHistoryMu.Lock()
	defer taskHistoryMu.Unlock()
	baselines, err := geodataHashBaselinesLocked()
	if err != nil {
		return nil, err
	}
	undo := make(map[string]*geodataHashBaseline)
	comparisons := make([]GeodataHashComparison, 0, len(samples))
	for _, sample := range samples {
		key := geodataHashBaselinePath(sample.path)
		previous, exists := baselines[key]
		comparison := GeodataHashComparison{File: sample.file, CurrentHash: sample.hash, PreviousHash: previous.Hash, BaselineAt: previous.ConfirmedAt}
		if previous.ConfirmedAt != 0 {
			comparison.BaselineTimezone = executionTimezone(timezone, time.UnixMilli(previous.ConfirmedAt))
		}
		if sample.err != nil {
			comparison.Status, comparison.Error = "unavailable", sample.err.Error()
		} else {
			comparison.Status = "baseline"
			if exists {
				comparison.Status = "changed"
				if previous.Hash == sample.hash {
					comparison.Status = "unchanged"
				}
				copy := previous
				undo[key] = &copy
			} else {
				undo[key] = nil
			}
			baselines[key] = geodataHashBaseline{Hash: sample.hash, ConfirmedAt: confirmedAt, RunID: record.ID}
		}
		comparisons = append(comparisons, comparison)
	}
	record.HashComparisons = comparisons
	if err := saveGeodataHashStateLocked(*record, baselines); err != nil {
		return nil, err
	}
	return undo, nil
}

func rollbackGeodataHashes(record TaskExecutionLog, undo map[string]*geodataHashBaseline) error {
	taskHistoryMu.Lock()
	defer taskHistoryMu.Unlock()
	baselines, err := geodataHashBaselinesLocked()
	if err != nil {
		return err
	}
	for path, previous := range undo {
		if baselines[path].RunID != record.ID {
			continue
		}
		if previous == nil {
			delete(baselines, path)
		} else {
			baselines[path] = *previous
		}
	}
	return saveGeodataHashStateLocked(record, baselines)
}
