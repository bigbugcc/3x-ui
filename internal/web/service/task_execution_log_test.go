package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestTaskLogsMigrateLegacyPruneAndClearWithoutResurrecting(t *testing.T) {
	setupSettingTestDB(t)
	now := time.Now()
	legacy := []XrayRestartRun{{StartedAt: now.Add(-48 * time.Hour).UnixMilli(), Status: "success", Trigger: "scheduled"}, {StartedAt: now.AddDate(0, 0, -40).UnixMilli(), Status: "failed", Trigger: "scheduled"}}
	encoded, _ := json.Marshal(legacy)
	if err := (&SettingService{}).saveSetting(restartHistoryKey, string(encoded)); err != nil {
		t.Fatal(err)
	}
	view, err := GetTaskExecutionLogs()
	if err != nil || len(view.History) != 1 || view.History[0].Task != "restart" || view.Settings.RetentionDays != 30 {
		t.Fatalf("legacy migration = %+v, %v", view, err)
	}
	geo := TaskExecutionLog{ID: "geo-test", XrayRestartRun: XrayRestartRun{StartedAt: now.UnixMilli(), Trigger: "scheduled", Status: "failed", Error: "Geo download failed"}, Task: "geodata", Timezone: "UTC"}
	if err := appendTaskExecutionLog(geo); err != nil {
		t.Fatal(err)
	}
	if err := SaveTaskLogSettings(TaskLogSettings{RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	view, err = GetTaskExecutionLogs()
	if err != nil || len(view.History) != 1 || !reflect.DeepEqual(view.History[0], geo) {
		t.Fatalf("prune = %+v, %v", view, err)
	}
	for _, days := range []int{0, -1, 366} {
		if err := SaveTaskLogSettings(TaskLogSettings{RetentionDays: days}); err == nil {
			t.Fatalf("accepted retention %d", days)
		}
	}
	if err := ClearTaskExecutionLogs(); err != nil {
		t.Fatal(err)
	}
	view, err = GetTaskExecutionLogs()
	if err != nil || len(view.History) != 0 || view.Settings.RetentionDays != 1 {
		t.Fatalf("clear = %+v, %v", view, err)
	}
	var old model.Setting
	if err := database.GetDB().Where("key = ?", restartHistoryKey).First(&old).Error; err != nil || old.Value != "[]" {
		t.Fatalf("legacy retained cleared logs: %+v, %v", old, err)
	}
}

func TestTaskLogsSameTimestampConcurrentRunsRemainDistinct(t *testing.T) {
	setupSettingTestDB(t)
	startedAt := time.Now().UnixMilli()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			if err := appendTaskExecutionLog(TaskExecutionLog{XrayRestartRun: XrayRestartRun{StartedAt: startedAt, Status: "success", Trigger: "manual"}, Task: "restart"}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	view, err := GetTaskExecutionLogs()
	if err != nil || len(view.History) != 16 {
		t.Fatalf("concurrent results lost: %+v, %v", view, err)
	}
	ids := map[string]bool{}
	for _, entry := range view.History {
		if entry.ID == "" || ids[entry.ID] {
			t.Fatalf("duplicate ID %q", entry.ID)
		}
		ids[entry.ID] = true
	}
}

func TestTaskLogRetentionFailureRollsBackSettingsAndHistory(t *testing.T) {
	setupSettingTestDB(t)
	record := TaskExecutionLog{XrayRestartRun: XrayRestartRun{StartedAt: time.Now().AddDate(0, 0, -14).UnixMilli(), Status: "success"}, Task: "restart"}
	if err := appendTaskExecutionLog(record); err != nil {
		t.Fatal(err)
	}
	const callback = "test:reject_task_history"
	rejected := fmt.Errorf("injected history write failure")
	if err := database.GetDB().Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if setting, ok := tx.Statement.Model.(*model.Setting); ok && setting.Key == taskHistoryKey {
			tx.AddError(rejected)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.GetDB().Callback().Update().Remove(callback) })
	if err := SaveTaskLogSettings(TaskLogSettings{RetentionDays: 1}); !errors.Is(err, rejected) {
		t.Fatalf("save error = %v", err)
	}
	view, err := GetTaskExecutionLogs()
	if err != nil || view.Settings.RetentionDays != 30 || len(view.History) != 1 {
		t.Fatalf("partial save = %+v, %v", view, err)
	}
}

func TestTaskLogsPanelRestartFinalizesPendingGeoWithoutClaimingSuccess(t *testing.T) {
	setupSettingTestDB(t)
	if err := appendTaskExecutionLog(TaskExecutionLog{XrayRestartRun: XrayRestartRun{StartedAt: time.Now().UnixMilli(), Status: "running"}, Task: "geodata"}); err != nil {
		t.Fatal(err)
	}
	_ = NewGeodataExecutionMonitor()
	view, err := GetTaskExecutionLogs()
	if err != nil || len(view.History) != 1 || view.History[0].Status != "unconfirmed" || view.History[0].Details != "geoCoreStopped" {
		t.Fatalf("orphaned execution = %+v, %v", view, err)
	}
}

func TestTaskLogsCompletionUpdatesRecordInsteadOfDuplicatingIt(t *testing.T) {
	setupSettingTestDB(t)
	record := TaskExecutionLog{ID: "geo-completion", XrayRestartRun: XrayRestartRun{StartedAt: time.Now().UnixMilli(), Trigger: "scheduled", Status: "running"}, Task: "geodata", Timezone: "UTC"}
	if err := appendTaskExecutionLog(record); err != nil {
		t.Fatal(err)
	}
	record.Status = "updated"
	record.Details = "geoFilesUpdated"
	if err := appendTaskExecutionLog(record); err != nil {
		t.Fatal(err)
	}
	view, err := GetTaskExecutionLogs()
	if err != nil || len(view.History) != 1 || !reflect.DeepEqual(view.History[0], record) {
		t.Fatalf("completion = %+v, %v", view, err)
	}
}

func TestLegacyTaskLogsUseCurrentTimezoneWithoutPersistingAnInventedSnapshot(t *testing.T) {
	setupSettingTestDB(t)
	s := &SettingService{}
	if err := s.saveSetting(restartScheduleKey, `{"timezone":"Asia/Shanghai"}`); err != nil {
		t.Fatal(err)
	}
	if err := appendTaskExecutionLog(TaskExecutionLog{ID: "legacy-zone", Task: "restart", XrayRestartRun: XrayRestartRun{StartedAt: time.Now().UnixMilli(), Status: "success"}}); err != nil {
		t.Fatal(err)
	}
	view, err := GetTaskExecutionLogs()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(view.History)
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	if err := json.Unmarshal(encoded, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0]["timezone"] != "Asia/Shanghai" || records[0]["timezoneInferred"] != true {
		t.Fatalf("legacy timezone not identified: %s", encoded)
	}
	stored, err := s.getSetting(taskHistoryKey)
	if err != nil || strings.Contains(stored.Value, `"timezone"`) {
		t.Fatalf("invented historical timezone persisted: %s, %v", stored.Value, err)
	}
}
