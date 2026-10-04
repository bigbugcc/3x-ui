package service

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestGeodataScheduleImportsLegacyAndPatchesLatestTemplate(t *testing.T) {
	setupSettingTestDB(t)
	dir := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", dir)
	if err := os.WriteFile(filepath.Join(dir, "geosite.dat"), []byte("test asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &XraySettingService{}
	initial := `{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"domainStrategy":"AsIs"},"geodata":{"cron":"0 4 * * *","assets":[{"url":"https://example.com/geosite.dat","file":"geosite.dat"}]}}`
	if err := s.saveSetting("xrayTemplateConfig", initial); err != nil {
		t.Fatal(err)
	}
	view, err := s.GetGeodataSchedule()
	if err != nil || !view.Config.Enabled || view.Config.Timezone != "Local" || view.NextRun == 0 {
		t.Fatalf("legacy view = %+v, %v", view, err)
	}
	plan := view.Config
	plan.Timezone = "Asia/Shanghai"
	// Another editor's unrelated change must survive the plan save.
	latest := strings.Replace(initial, `"AsIs"`, `"IPIfNonMatch"`, 1)
	if err := s.saveSetting("xrayTemplateConfig", latest); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveGeodataSchedule(plan); err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetXrayConfigTemplate()
	if err != nil || !strings.Contains(stored, "IPIfNonMatch") || !strings.Contains(stored, "CRON_TZ=Asia/Shanghai 0 4 * * *") {
		t.Fatalf("stored template = %s, %v", stored, err)
	}
	plan.Enabled = false
	if err := s.SaveGeodataSchedule(plan); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.GetXrayConfigTemplate()
	var cfg map[string]any
	_ = json.Unmarshal([]byte(stored), &cfg)
	if _, exists := cfg["geodata"]; exists {
		t.Fatal("disabled plan left core geodata schedule")
	}
	view, err = s.GetGeodataSchedule()
	if err != nil || view.Config.Enabled || len(view.Config.Assets) != 1 || view.Config.Timezone != plan.Timezone || view.NextRun != 0 {
		t.Fatalf("disabled draft = %+v, %v", view, err)
	}
}

func TestGeodataScheduleValidationPreservesTemplate(t *testing.T) {
	setupSettingTestDB(t)
	t.Setenv("XUI_BIN_FOLDER", t.TempDir())
	s := &XraySettingService{}
	original := `{"outbounds":[]}`
	if err := s.saveSetting("xrayTemplateConfig", original); err != nil {
		t.Fatal(err)
	}
	valid := GeodataUpdateSchedule{Enabled: true, Cron: "0 4 * * *", Timezone: "UTC", Assets: []GeodataSource{{URL: "https://example.com/geosite.dat", File: "geosite.dat"}}}
	for _, mutate := range []func(*GeodataUpdateSchedule){
		func(p *GeodataUpdateSchedule) { p.Cron = "0 0 4 * * *" },
		func(p *GeodataUpdateSchedule) { p.Timezone = "invalid" },
		func(p *GeodataUpdateSchedule) { p.Assets[0].URL = "http://example.com/geosite.dat" },
		func(p *GeodataUpdateSchedule) { p.Assets[0].File = "../geosite.dat" },
		func(p *GeodataUpdateSchedule) { p.Assets = append(p.Assets, p.Assets[0]) },
		func(p *GeodataUpdateSchedule) {}, // Missing asset on disk.
	} {
		plan := valid
		plan.Assets = append([]GeodataSource{}, valid.Assets...)
		mutate(&plan)
		if err := s.SaveGeodataSchedule(plan); err == nil {
			t.Errorf("accepted %+v", plan)
		}
		stored, _ := s.GetXrayConfigTemplate()
		if stored != original {
			t.Fatal("rejected input changed template")
		}
	}
	valid.Enabled = false
	if err := s.SaveGeodataSchedule(valid); err != nil {
		t.Fatalf("could not save disabled draft with missing files: %v", err)
	}
}

func TestGeodataScheduleTransactionRollsBackBothSettings(t *testing.T) {
	setupSettingTestDB(t)
	s := &XraySettingService{}
	original := `{"geodata":{"cron":"0 4 * * *","assets":[]},"routing":{"domainStrategy":"AsIs"}}`
	if err := s.saveSetting("xrayTemplateConfig", original); err != nil {
		t.Fatal(err)
	}
	const callback = "test:reject_geo_draft"
	errInjected := errors.New("draft write failed")
	if err := database.GetDB().Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if setting, ok := tx.Statement.Dest.(*model.Setting); ok && setting.Key == geodataScheduleDraftKey {
			tx.AddError(errInjected)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.GetDB().Callback().Create().Remove(callback) })
	if err := s.SaveGeodataSchedule(GeodataUpdateSchedule{Cron: "0 5 * * *", Timezone: "UTC"}); !errors.Is(err, errInjected) {
		t.Fatalf("error = %v", err)
	}
	stored, _ := s.GetXrayConfigTemplate()
	if stored != original {
		t.Fatal("template escaped failed transaction")
	}
}
