package service

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const taskHistoryKey = "taskExecutionHistory"
const taskLogSettingsKey = "taskExecutionLogSettings"
const maxTaskHistory = 5000

var taskHistoryMu sync.Mutex

type TaskLogSettings struct {
	RetentionDays int `json:"retentionDays" validate:"min=1,max=365" example:"30"`
}

type TaskExecutionLog struct {
	XrayRestartRun
	ID               string                  `json:"id"`
	Task             string                  `json:"task"`
	Details          string                  `json:"details"`
	Timezone         string                  `json:"timezone,omitempty"`
	TimezoneInferred bool                    `json:"timezoneInferred,omitempty"`
	HashComparisons  []GeodataHashComparison `json:"hashComparisons,omitempty"`
}

type GeodataHashComparison struct {
	File             string `json:"file" example:"geosite.dat"`
	Status           string `json:"status" example:"changed"`
	PreviousHash     string `json:"previousHash,omitempty"`
	CurrentHash      string `json:"currentHash,omitempty"`
	BaselineAt       int64  `json:"baselineAt,omitempty"`
	BaselineTimezone string `json:"baselineTimezone,omitempty"`
	Error            string `json:"error,omitempty"`
}

type TaskExecutionLogView struct {
	Settings TaskLogSettings    `json:"settings"`
	History  []TaskExecutionLog `json:"history"`
}

func taskLogSettingsLocked() (TaskLogSettings, error) {
	cfg := TaskLogSettings{RetentionDays: 30}
	stored, err := (&SettingService{}).getSetting(taskLogSettingsKey)
	if database.IsNotFound(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal([]byte(stored.Value), &cfg)
	return cfg, err
}

// Import the old restart history once; subsequent reads use the shared store.
func taskHistoryLocked() ([]TaskExecutionLog, error) {
	history := []TaskExecutionLog{}
	settings := &SettingService{}
	stored, err := settings.getSetting(taskHistoryKey)
	if database.IsNotFound(err) {
		legacy, legacyErr := settings.getSetting(restartHistoryKey)
		if database.IsNotFound(legacyErr) {
			return history, nil
		}
		if legacyErr != nil {
			return nil, legacyErr
		}
		var runs []XrayRestartRun
		if err := json.Unmarshal([]byte(legacy.Value), &runs); err != nil {
			return nil, err
		}
		for _, run := range runs {
			history = append(history, TaskExecutionLog{XrayRestartRun: run, ID: uuid.NewString(), Task: "restart"})
		}
		return history, nil
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(stored.Value), &history)
	return history, err
}

func prunedTaskHistory(history []TaskExecutionLog, cfg TaskLogSettings) []TaskExecutionLog {
	sort.SliceStable(history, func(i, j int) bool { return history[i].StartedAt > history[j].StartedAt })
	cutoff := time.Now().AddDate(0, 0, -cfg.RetentionDays).UnixMilli()
	kept := make([]TaskExecutionLog, 0, len(history))
	for _, record := range history {
		if record.StartedAt >= cutoff && len(kept) < maxTaskHistory {
			kept = append(kept, record)
		}
	}
	return kept
}

func writeTaskLogSetting(tx *gorm.DB, key, value string) error {
	setting := model.Setting{Key: key}
	if err := tx.Where("key = ?", key).First(&setting).Error; err != nil {
		if !database.IsNotFound(err) {
			return err
		}
		setting.Value = value
		return tx.Create(&setting).Error
	}
	if setting.Value == value {
		return nil
	}
	setting.Value = value
	return tx.Save(&setting).Error
}

func writeTaskHistory(tx *gorm.DB, history []TaskExecutionLog) error {
	encoded, err := json.Marshal(history)
	if err != nil {
		return err
	}
	if err := writeTaskLogSetting(tx, taskHistoryKey, string(encoded)); err != nil {
		return err
	}
	return writeTaskLogSetting(tx, restartHistoryKey, "[]")
}

func saveTaskHistoryLocked(history []TaskExecutionLog, cfg TaskLogSettings) error {
	history = prunedTaskHistory(history, cfg)
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		return writeTaskHistory(tx, history)
	})
}

func upsertTaskExecutionLog(history []TaskExecutionLog, record TaskExecutionLog) []TaskExecutionLog {
	for i := range history {
		if history[i].ID == record.ID {
			history[i] = record
			return history
		}
	}
	return append([]TaskExecutionLog{record}, history...)
}

func appendTaskExecutionLog(record TaskExecutionLog) error {
	if record.ID == "" {
		record.ID = uuid.NewString()
	}
	taskHistoryMu.Lock()
	defer taskHistoryMu.Unlock()
	cfg, err := taskLogSettingsLocked()
	if err != nil {
		return err
	}
	history, err := taskHistoryLocked()
	if err != nil {
		return err
	}
	return saveTaskHistoryLocked(upsertTaskExecutionLog(history, record), cfg)
}

func GetTaskExecutionLogs() (*TaskExecutionLogView, error) {
	taskHistoryMu.Lock()
	defer taskHistoryMu.Unlock()
	cfg, err := taskLogSettingsLocked()
	if err != nil {
		return nil, err
	}
	history, err := taskHistoryLocked()
	if err != nil {
		return nil, err
	}
	history = prunedTaskHistory(history, cfg)
	if err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		return writeTaskHistory(tx, history)
	}); err != nil {
		return nil, err
	}
	inferTaskLogTimezones(history)
	return &TaskExecutionLogView{Settings: cfg, History: history}, nil
}

func SaveTaskLogSettings(cfg TaskLogSettings) error {
	if cfg.RetentionDays < 1 || cfg.RetentionDays > 365 {
		return errors.New("log retention must be between 1 and 365 days")
	}
	taskHistoryMu.Lock()
	defer taskHistoryMu.Unlock()
	history, err := taskHistoryLocked()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	history = prunedTaskHistory(history, cfg)
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := writeTaskLogSetting(tx, taskLogSettingsKey, string(encoded)); err != nil {
			return err
		}
		return writeTaskHistory(tx, history)
	})
}

func ClearTaskExecutionLogs() error {
	taskHistoryMu.Lock()
	defer taskHistoryMu.Unlock()
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		return writeTaskHistory(tx, []TaskExecutionLog{})
	})
}
