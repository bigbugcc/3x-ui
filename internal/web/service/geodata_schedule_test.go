package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xrayconf "github.com/xtls/xray-core/infra/conf"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

type geodataScheduleTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (tr geodataScheduleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.URL.Scheme, copy.URL.Host = tr.target.Scheme, tr.target.Host
	return tr.base.RoundTrip(copy)
}

func TestGeodataScheduleInitializationCancellation(t *testing.T) {
	for _, stage := range []string{"redirect", "download", "checksum"} {
		for _, deadline := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/deadline=%t", stage, deadline), func(t *testing.T) {
				dir := geofileTestEnv(t, map[string]geofileEntry{"geoip.dat": {"https://standard.test/a", "geoip.dat", "geoip.dat"}})
				s := &XraySettingService{}
				const original = `{"outbounds":[]}`
				if err := s.saveSetting("xrayTemplateConfig", original); err != nil {
					t.Fatal(err)
				}
				started, canceled := make(chan struct{}), make(chan struct{})
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					current := "download"
					if strings.Contains(r.URL.Path, "/latest/") {
						current = "redirect"
					} else if strings.HasSuffix(r.URL.Path, ".sha256sum") {
						current = "checksum"
					}
					if current == stage {
						if stage == "download" {
							// Also exercise cancellation while consuming a response body.
							_, _ = w.Write([]byte("partial"))
							w.(http.Flusher).Flush()
						}
						close(started)
						select {
						case <-r.Context().Done():
							close(canceled)
						case <-time.After(2 * time.Second):
						}
						return
					}
					switch current {
					case "redirect":
						http.Redirect(w, r, "/a/releases/download/v1/geoip.dat", http.StatusFound)
					case "download":
						_, _ = w.Write([]byte("payload"))
					case "checksum":
						_, _ = fmt.Fprintf(w, "%x  geoip.dat\n", sha256.Sum256([]byte("payload")))
					}
				}))
				defer srv.Close()
				target, _ := url.Parse(srv.URL)
				originalTransport := http.DefaultTransport
				http.DefaultTransport = geodataScheduleTransport{target, originalTransport}
				defer func() { http.DefaultTransport = originalTransport }()
				var ctx context.Context
				var cancel context.CancelFunc
				if deadline {
					ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
				} else {
					ctx, cancel = context.WithCancel(context.Background())
				}
				defer cancel()
				done := make(chan error, 1)
				go func() {
					done <- s.SaveGeodataSchedule(ctx, GeodataUpdateSchedule{Enabled: true, Cron: "0 4 * * *", Timezone: "UTC", Assets: StandardGeodataSources()})
				}()
				select {
				case <-started:
				case <-time.After(2 * time.Second):
					t.Fatal("save did not reach the expected download stage")
				}
				if !deadline {
					cancel()
				}
				select {
				case err := <-done:
					if !errors.Is(err, ctx.Err()) || ctx.Err() == nil {
						t.Fatalf("canceled save returned %v, want %v", err, ctx.Err())
					}
				case <-time.After(time.Second):
					t.Fatal("download outlived the request cancellation")
				}
				select {
				case <-canceled:
				case <-time.After(time.Second):
					t.Fatal("upstream did not observe request cancellation")
				}
				stored, err := s.GetXrayConfigTemplate()
				if err != nil || stored != original {
					t.Fatalf("canceled initialization changed the template: %s, %v", stored, err)
				}
				if _, err := s.getSetting(geodataScheduleDraftKey); !database.IsNotFound(err) {
					t.Fatalf("canceled initialization saved a draft: %v", err)
				}
				files, err := os.ReadDir(dir)
				if err != nil || len(files) != 0 {
					t.Fatalf("canceled initialization left files or staging directories: %v, %v", files, err)
				}
			})
		}
	}
}

func TestGeodataScheduleCancellationRollsBackTransaction(t *testing.T) {
	setupSettingTestDB(t)
	s := &XraySettingService{}
	const original = `{"outbounds":[]}`
	if err := s.saveSetting("xrayTemplateConfig", original); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const callback = "test:cancel_geo_save"
	if err := database.GetDB().Callback().Create().After("gorm:create").Register(callback, func(tx *gorm.DB) {
		if setting, ok := tx.Statement.Dest.(*model.Setting); ok && setting.Key == geodataScheduleDraftKey {
			cancel()
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.GetDB().Callback().Create().Remove(callback) })
	if err := s.SaveGeodataSchedule(ctx, GeodataUpdateSchedule{Cron: "0 5 * * *", Timezone: "UTC"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("transaction cancellation returned %v", err)
	}
	stored, err := s.GetXrayConfigTemplate()
	if err != nil || stored != original {
		t.Fatalf("canceled transaction changed the template: %s, %v", stored, err)
	}
	if _, err := s.getSetting(geodataScheduleDraftKey); !database.IsNotFound(err) {
		t.Fatalf("canceled transaction saved the draft: %v", err)
	}
}

func TestGeodataScheduleInitializesMissingStandardFiles(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprintf("corrupt=%t", corrupt), func(t *testing.T) {
			srv, hits := geofileServer(t, []fakeUpstream{{repo: "a", assets: map[string]string{"geoip.dat": "verified ip", "geosite.dat": "verified site"}, corrupt: map[string]bool{"geosite.dat": corrupt}}})
			entries := map[string]geofileEntry{
				"geoip.dat":   {"https://standard.test/a", "geoip.dat", "geoip.dat"},
				"geosite.dat": {"https://standard.test/a", "geosite.dat", "geosite.dat"},
			}
			dir := geofileTestEnv(t, entries)
			dir = filepath.Join(dir, "new-assets")
			t.Setenv("XUI_BIN_FOLDER", dir)
			t.Setenv("XRAY_LOCATION_ASSET", dir)
			target, _ := url.Parse(srv.URL)
			originalTransport := http.DefaultTransport
			http.DefaultTransport = geodataScheduleTransport{target, originalTransport}
			t.Cleanup(func() { http.DefaultTransport = originalTransport })
			s := &XraySettingService{}
			const original = `{"outbounds":[]}`
			if err := s.saveSetting("xrayTemplateConfig", original); err != nil {
				t.Fatal(err)
			}
			plan := GeodataUpdateSchedule{Enabled: true, Cron: "0 4 * * *", Timezone: "UTC", Assets: StandardGeodataSources()}
			var restarted bool
			restartStub(t, &restarted)
			err := s.SaveGeodataSchedule(context.Background(), plan)
			if _, ok := hits.Load("body:a/geoip.dat"); !ok {
				t.Fatal("initialization never downloaded the first file")
			}
			if corrupt {
				if err == nil {
					t.Fatal("accepted corrupt initial download")
				}
				stored, _ := s.GetXrayConfigTemplate()
				if stored != original {
					t.Fatal("failed initialization changed the plan")
				}
				for name := range entries {
					if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
						t.Fatalf("installed %s before all downloads verified: %v", name, err)
					}
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				stored, err := s.GetXrayConfigTemplate()
				if err != nil {
					t.Fatal(err)
				}
				var native xrayconf.Config
				if err := json.Unmarshal([]byte(stored), &native); err != nil {
					t.Fatal(err)
				}
				if _, err := native.Build(); err != nil {
					t.Fatalf("Xray rejected initialized plan: %v", err)
				}
				for name, want := range map[string]string{"geoip.dat": "verified ip", "geosite.dat": "verified site"} {
					got, err := os.ReadFile(filepath.Join(dir, name))
					if err != nil || string(got) != want {
						t.Fatalf("%s: %q, %v", name, got, err)
					}
				}
				// Saving again must not fetch or replace an existing administrator file.
				if err := os.WriteFile(filepath.Join(dir, "geoip.dat"), []byte("manual change"), 0600); err != nil {
					t.Fatal(err)
				}
				srv.Close()
				if err := s.SaveGeodataSchedule(context.Background(), plan); err != nil {
					t.Fatalf("existing files caused a download: %v", err)
				}
				got, _ := os.ReadFile(filepath.Join(dir, "geoip.dat"))
				if string(got) != "manual change" {
					t.Fatal("overwrote existing file")
				}
			}
			if restarted {
				t.Fatal("initialization restarted Xray before the controller applies the saved plan")
			}
		})
	}
}

func TestGeodataScheduleInitializationFailuresKeepPlanDisabled(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(fmt.Sprintf("unavailable=%t", unavailable), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/latest/") {
					http.Redirect(w, r, "/a/releases/download/v1/geoip.dat", http.StatusFound)
					return
				}
				w.WriteHeader(http.StatusNotModified)
			}))
			t.Cleanup(srv.Close)
			dir := geofileTestEnv(t, map[string]geofileEntry{"geoip.dat": {"https://standard.test/a", "geoip.dat", "geoip.dat"}})
			target, _ := url.Parse(srv.URL)
			originalTransport := http.DefaultTransport
			http.DefaultTransport = geodataScheduleTransport{target, originalTransport}
			t.Cleanup(func() { http.DefaultTransport = originalTransport })
			if unavailable {
				srv.Close()
			}
			s := &XraySettingService{}
			const original = `{"outbounds":[]}`
			if err := s.saveSetting("xrayTemplateConfig", original); err != nil {
				t.Fatal(err)
			}
			err := s.SaveGeodataSchedule(context.Background(), GeodataUpdateSchedule{Enabled: true, Cron: "0 4 * * *", Timezone: "UTC", Assets: StandardGeodataSources()})
			var scheduleErr *GeodataScheduleError
			if !errors.As(err, &scheduleErr) {
				t.Fatalf("expected localized initialization error: %v", err)
			}
			stored, _ := s.GetXrayConfigTemplate()
			if stored != original {
				t.Fatal("failed initialization enabled the plan")
			}
			if _, err := os.Stat(filepath.Join(dir, "geoip.dat")); !os.IsNotExist(err) {
				t.Fatalf("unexpected installed file: %v", err)
			}
		})
	}
}

func TestGeodataScheduleDoesNotBootstrapCustomSources(t *testing.T) {
	setupSettingTestDB(t)
	dir := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", dir)
	for _, file := range []string{"geoip.dat", "custom.dat"} {
		err := (&XraySettingService{}).SaveGeodataSchedule(context.Background(), GeodataUpdateSchedule{Enabled: true, Cron: "0 4 * * *", Timezone: "UTC", Assets: []GeodataSource{{URL: "https://example.invalid/geoip.dat", File: file}}})
		var scheduleErr *GeodataScheduleError
		if !errors.As(err, &scheduleErr) || scheduleErr.Key != "geoFileRequired" || scheduleErr.File != file {
			t.Fatalf("custom source: %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("custom source caused filesystem changes: %v, %v", entries, err)
	}
}

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
	if err := s.SaveGeodataSchedule(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetXrayConfigTemplate()
	if err != nil || !strings.Contains(stored, "IPIfNonMatch") || !strings.Contains(stored, "CRON_TZ=Asia/Shanghai 0 4 * * *") {
		t.Fatalf("stored template = %s, %v", stored, err)
	}
	plan.Enabled = false
	if err := s.SaveGeodataSchedule(context.Background(), plan); err != nil {
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
		if err := s.SaveGeodataSchedule(context.Background(), plan); err == nil {
			t.Errorf("accepted %+v", plan)
		}
		stored, _ := s.GetXrayConfigTemplate()
		if stored != original {
			t.Fatal("rejected input changed template")
		}
	}
	valid.Enabled = false
	if err := s.SaveGeodataSchedule(context.Background(), valid); err != nil {
		t.Fatalf("could not save disabled draft with missing files: %v", err)
	}
}

func TestGeodataScheduleRejectsEnabledPlanWithoutDownloads(t *testing.T) {
	setupSettingTestDB(t)
	s := &XraySettingService{}
	const original = `{"outbounds":[]}`
	if err := s.saveSetting("xrayTemplateConfig", original); err != nil {
		t.Fatal(err)
	}
	plan := GeodataUpdateSchedule{Enabled: true, Cron: "0 4 * * *", Timezone: "Asia/Shanghai"}
	if err := s.SaveGeodataSchedule(context.Background(), plan); err == nil || err.Error() != "at least one geodata download file is required before enabling updates" {
		t.Fatalf("empty enabled plan: %v", err)
	}
	stored, err := s.GetXrayConfigTemplate()
	if err != nil || stored != original {
		t.Fatalf("rejected plan changed template: %s, %v", stored, err)
	}
	plan.Enabled = false
	if err := s.SaveGeodataSchedule(context.Background(), plan); err != nil {
		t.Fatalf("disabled draft: %v", err)
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
	if err := s.SaveGeodataSchedule(context.Background(), GeodataUpdateSchedule{Cron: "0 5 * * *", Timezone: "UTC"}); !errors.Is(err, errInjected) {
		t.Fatalf("error = %v", err)
	}
	stored, _ := s.GetXrayConfigTemplate()
	if stored != original {
		t.Fatal("template escaped failed transaction")
	}
}
