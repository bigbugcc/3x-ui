package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func newTestXrayScheduler(t *testing.T) *XrayScheduler {
	t.Helper()
	c := cron.New(cron.WithSeconds())
	c.Start()
	t.Cleanup(func() { <-c.Stop().Done() })
	return NewXrayScheduler(context.Background(), c)
}

func TestMaintenanceScheduleTimezoneAndValidation(t *testing.T) {
	schedule, err := ParseMaintenanceSchedule("0 4 * * *", "Asia/Shanghai", false)
	if err != nil {
		t.Fatal(err)
	}
	next := schedule.Next(time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC))
	want := time.Date(2026, 10, 3, 20, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
	for _, spec := range []string{"0 0 4 * * *", "99 4 * * *", "0 4 31 2 *", "@every 30s", "@daily", "CRON_TZ=UTC 0 4 * * *"} {
		if _, err := ParseMaintenanceSchedule(spec, "UTC", true); err == nil {
			t.Errorf("accepted %q", spec)
		}
	}
	if _, err := ParseMaintenanceSchedule("0 4 * * *", "bad-zone", true); err == nil {
		t.Error("accepted invalid timezone")
	}
	if _, err := ParseMaintenanceSchedule("@every 1m", "UTC", false); err == nil {
		t.Error("geodata accepted interval")
	}
	if _, err := ParseMaintenanceSchedule("@every 1h", "UTC", true); err != nil {
		t.Fatal(err)
	}
}

func TestRestartSchedulePersistenceReplacementAndDisable(t *testing.T) {
	setupSettingTestDB(t)
	s := newTestXrayScheduler(t)
	if err := s.Restore(); err != nil {
		t.Fatal(err)
	}
	view, err := s.View()
	if err != nil || view.Config.Enabled || view.NextRun != 0 || len(s.cron.Entries()) != 0 {
		t.Fatalf("unexpected defaults: %+v, %v", view, err)
	}
	plan := XrayRestartSchedule{Enabled: true, Cron: "0 4 * * 0", Timezone: "Asia/Shanghai"}
	if err := s.Save(plan); err != nil {
		t.Fatal(err)
	}
	oldGeneration := s.generation
	plan.Cron = "0 5 * * *"
	if err := s.Save(plan); err != nil {
		t.Fatal(err)
	}
	if len(s.cron.Entries()) != 1 {
		t.Fatal("replacement left duplicate entries")
	}
	view, err = s.View()
	if err != nil || view.NextRun <= time.Now().UnixMilli() {
		t.Fatalf("no upcoming execution: %+v, %v", view, err)
	}
	restored := newTestXrayScheduler(t)
	if err := restored.Restore(); err != nil {
		t.Fatal(err)
	}
	if restored.config != plan || len(restored.cron.Entries()) != 1 {
		t.Fatal("plan not restored")
	}
	plan.Enabled = false
	if err := s.Save(plan); err != nil {
		t.Fatal(err)
	}
	s.restart = func() (bool, error) { t.Fatal("disabled or replaced job ran"); return true, nil }
	if record, err := s.run("scheduled", oldGeneration); record != nil || err != nil {
		t.Fatalf("stale run = %+v, %v", record, err)
	}
	if len(s.cron.Entries()) != 0 {
		t.Fatal("disabled job retained entry")
	}
}

func TestRestartScheduleFailedSaveKeepsActivePlan(t *testing.T) {
	setupSettingTestDB(t)
	s := newTestXrayScheduler(t)
	plan := XrayRestartSchedule{Enabled: true, Cron: "0 4 * * *", Timezone: "UTC"}
	if err := s.Save(plan); err != nil {
		t.Fatal(err)
	}
	entry := s.entry
	const callback = "test:reject_restart_plan"
	errInjected := errors.New("injected schedule write failure")
	if err := database.GetDB().Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if setting, ok := tx.Statement.Model.(*model.Setting); ok && setting.Key == restartScheduleKey {
			tx.AddError(errInjected)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.GetDB().Callback().Update().Remove(callback) })
	changed := plan
	changed.Cron = "0 5 * * *"
	if err := s.Save(changed); !errors.Is(err, errInjected) {
		t.Fatalf("save error = %v", err)
	}
	if s.config != plan || s.entry != entry {
		t.Fatal("failed persistence changed active plan")
	}
}

func TestRestartScheduleHistorySkipFailureAndLimit(t *testing.T) {
	setupSettingTestDB(t)
	s := newTestXrayScheduler(t)
	isManuallyStopped.Store(true)
	t.Cleanup(func() { isManuallyStopped.Store(false) })
	for i := 0; i < maxRestartHistory+2; i++ {
		record, err := s.RunNow()
		if err != nil || record.Status != "skipped" {
			t.Fatalf("manual-stop run = %+v, %v", record, err)
		}
	}
	if !isManuallyStopped.Load() {
		t.Fatal("task revived manually stopped Xray")
	}
	s.restart = func() (bool, error) { return true, errors.New("test restart failed") }
	record, err := s.RunNow()
	if err == nil || record.Status != "failed" {
		t.Fatalf("failed run = %+v, %v", record, err)
	}
	view, err := s.View()
	if err != nil || len(view.History) != maxRestartHistory || !strings.Contains(view.History[0].Error, "test restart failed") {
		t.Fatalf("history = %+v, %v", view, err)
	}
	restored := newTestXrayScheduler(t)
	view, err = restored.View()
	if err != nil || len(view.History) != maxRestartHistory {
		t.Fatal("history did not survive new scheduler")
	}
}

func TestRestartScheduleRejectsOverlappingRunsAndShutdown(t *testing.T) {
	setupSettingTestDB(t)
	s := newTestXrayScheduler(t)
	entered := make(chan struct{})
	finish := make(chan struct{})
	s.restart = func() (bool, error) { close(entered); <-finish; return true, nil }
	done := make(chan error, 1)
	go func() { _, err := s.RunNow(); done <- err }()
	<-entered
	view, err := s.View()
	if err != nil || !view.Running {
		t.Fatal("run not exposed")
	}
	if _, err := s.RunNow(); err == nil {
		t.Fatal("overlapping task accepted")
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.ctx = ctx
	if _, err := s.RunNow(); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown run = %v", err)
	}
}

func TestScheduledRestartReportsInvalidConfigAndGeoDoesNotReviveStoppedCore(t *testing.T) {
	setupSettingTestDB(t)
	isManuallyStopped.Store(false)
	t.Cleanup(func() { isManuallyStopped.Store(false) })
	// Invalid JSON exposes an attempted start instead of an unchanged-config no-op.
	if err := (&SettingService{}).saveSetting("xrayTemplateConfig", "{ invalid"); err != nil {
		t.Fatal(err)
	}
	if attempted, err := (&XrayService{}).RestartXrayScheduled(); !attempted || err == nil || !strings.Contains(err.Error(), "invalid character 'i'") {
		t.Fatalf("scheduled restart = %v, %v", attempted, err)
	}
	isManuallyStopped.Store(true)
	if attempted, err := (&XrayService{}).RestartXrayIfRunning(); attempted || err != nil {
		t.Fatalf("geodata revived a stopped core: %v, %v", attempted, err)
	}
}
