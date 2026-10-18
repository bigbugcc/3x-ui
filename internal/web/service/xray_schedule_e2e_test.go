package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	coregeodata "github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"

	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func TestXrayScheduleRealCoreLifecycle(t *testing.T) {
	binary := os.Getenv("XRAY_E2E_BINARY")
	if binary == "" {
		t.Skip("set XRAY_E2E_BINARY to verify the real core lifecycle")
	}
	setupSettingTestDB(t)
	dir := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", dir)
	t.Setenv("XUI_LOG_FOLDER", dir)
	t.Setenv("XRAY_LOCATION_ASSET", dir)
	t.Setenv("ZONEINFO", filepath.Join(runtime.GOROOT(), "lib", "time", "zoneinfo.zip"))
	binaryName := xray.GetBinaryName()
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	if err := os.Symlink(binary, filepath.Join(dir, binaryName)); err != nil {
		data, readErr := os.ReadFile(binary)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if copyErr := os.WriteFile(filepath.Join(dir, binaryName), data, 0o700); copyErr != nil {
			t.Fatal(copyErr)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf(`{"api":{"tag":"api","services":["HandlerService","StatsService","RoutingService"]},"inbounds":[{"tag":"api","listen":"127.0.0.1","port":%d,"protocol":"tunnel","settings":{"rewriteAddress":"127.0.0.1"}}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","inboundTag":["api"],"outboundTag":"api"}]},"log":{"loglevel":"error"},"stats":{}}`, port)
	settings := &XraySettingService{}
	if err := settings.SaveXraySetting(template); err != nil {
		t.Fatal(err)
	}
	previousStopped := isManuallyStopped.Load()
	restore := SetXrayProcessForTest(nil)
	svc := &XrayService{}
	t.Cleanup(func() {
		_ = svc.StopXray()
		restore()
		isManuallyStopped.Store(previousStopped)
	})
	if err := svc.RestartXray(true); err != nil {
		t.Fatal(err)
	}
	waitForMaintenanceCore(t, port)
	first := currentXrayProcess()
	scheduler := NewXrayScheduler(context.Background(), cron.New())
	if err := scheduler.Save(XrayRestartSchedule{Cron: "0 4 * * *", Timezone: "UTC", RestartOutbounds: []string{"direct"}}); err != nil {
		t.Fatal(err)
	}
	record, err := scheduler.RunNow()
	if err != nil || record.Status != "success" || len(record.Steps) != 1 || record.Steps[0].Target != "direct" || record.Steps[0].Status != "success" {
		t.Fatalf("real restart = %+v, %v", record, err)
	}
	waitForMaintenanceCore(t, port)
	second := currentXrayProcess()
	if second == first || first.IsRunning() {
		t.Fatal("scheduled task did not replace the running core")
	}
	plan := GeodataUpdateSchedule{Cron: "0 4 * * *", Timezone: "Asia/Shanghai", Assets: []GeodataSource{}}
	if err := settings.SaveGeodataSchedule(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if applied, err := svc.RestartXrayIfRunning(context.Background()); !applied || err != nil {
		t.Fatalf("draft apply = %v, %v", applied, err)
	}
	if currentXrayProcess() != second {
		t.Fatal("saving an unchanged disabled Geo plan interrupted connections")
	}
	data, err := proto.Marshal(&coregeodata.GeoSiteList{Entry: []*coregeodata.GeoSite{{Code: "example", Domain: []*coregeodata.Domain{{Type: coregeodata.Domain_Domain, Value: "example.com"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "geosite.dat"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	plan.Enabled = true
	plan.Assets = []GeodataSource{{URL: "https://example.com/geosite.dat", File: "geosite.dat"}}
	if err := settings.SaveGeodataSchedule(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if applied, err := svc.RestartXrayIfRunning(context.Background()); !applied || err != nil {
		t.Fatalf("Geo apply = %v, %v", applied, err)
	}
	waitForMaintenanceCore(t, port)
	if currentXrayProcess() == second || second.IsRunning() {
		t.Fatal("changed Geo plan did not restart the core")
	}
	// Default warning logs must still expose actual reload completion to the monitor.
	template, err = settings.GetXrayConfigTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var native map[string]any
	if err := json.Unmarshal([]byte(template), &native); err != nil {
		t.Fatal(err)
	}
	native["log"] = map[string]any{"loglevel": "warning"}
	native["geodata"] = map[string]any{"cron": "@every 1s", "assets": []any{}}
	apply := func() {
		t.Helper()
		raw, err := json.Marshal(native)
		if err != nil {
			t.Fatal(err)
		}
		if err := settings.SaveXraySetting(string(raw)); err != nil {
			t.Fatal(err)
		}
		if err := svc.RestartXray(true); err != nil {
			t.Fatal(err)
		}
		waitForMaintenanceCore(t, port)
		if err := ClearTaskExecutionLogs(); err != nil {
			t.Fatal(err)
		}
		persisted, err := os.ReadFile(xray.GetConfigPath())
		if err != nil {
			t.Fatal(err)
		}
		var desired map[string]json.RawMessage
		if err := json.Unmarshal(persisted, &desired); err != nil {
			t.Fatal(err)
		}
		actualLog, _ := json.Marshal(native["log"])
		var logFromDisk any
		_ = json.Unmarshal(desired["log"], &logFromDisk)
		diskLog, _ := json.Marshal(logFromDisk)
		if string(actualLog) != string(diskLog) {
			t.Fatalf("observation changed persisted log settings: %s != %s", actualLog, diskLog)
		}
	}
	assertGeo := func(status, details string) {
		t.Helper()
		monitor := &GeodataExecutionMonitor{}
		core := currentXrayProcess()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			monitor.Poll()
			logs, err := GetTaskExecutionLogs()
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range logs.History {
				if entry.Task == "geodata" && entry.Status == status && entry.Details == details {
					if entry.Timezone == "" || entry.TimezoneInferred {
						t.Fatalf("native execution lost its timezone: %+v", entry)
					}
					if currentXrayProcess() != core || !core.IsRunning() {
						t.Fatal("observing a native reload restarted the core")
					}
					return
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("native Geo completion %q was not recorded", details)
	}
	apply()
	assertGeo("skipped", "geoNoDownloads")
	native["log"] = map[string]any{"loglevel": "warning", "error": filepath.Join(dir, "native-error.log")}
	apply()
	assertGeo("skipped", "geoNoDownloads")
	native["log"] = map[string]any{"loglevel": "none"}
	apply()
	assertGeo("skipped", "geoNoDownloads")
	native["log"] = map[string]any{"loglevel": "error"}
	native["geodata"] = map[string]any{"cron": "@every 1s", "assets": []any{map[string]any{"file": "geosite.dat", "url": "https://127.0.0.1:1/geosite.dat"}}}
	apply()
	// A local closed port exercises native download failure without external network.
	monitor := &GeodataExecutionMonitor{}
	deadline := time.Now().Add(10 * time.Second)
	observedFailure := false
	for time.Now().Before(deadline) {
		monitor.Poll()
		logs, err := GetTaskExecutionLogs()
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range logs.History {
			if entry.Task == "geodata" && entry.Status == "failed" && strings.Contains(entry.Error, "scheduled geodata reload failed") {
				observedFailure = true
			}
		}
		if observedFailure {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !observedFailure {
		t.Fatal("native Geo failure was not recorded")
	}
	downloadObserved := false
	for _, event := range currentXrayProcess().GeodataEvents(0) {
		if event.Kind == "download" && event.Target == filepath.Join(dir, "geosite.dat") {
			downloadObserved = true
		}
	}
	if !downloadObserved {
		t.Fatal("native asset location was not captured")
	}
	if err := svc.StopXray(); err != nil {
		t.Fatal(err)
	}
	privateConfigs, err := filepath.Glob(filepath.Join(dir, ".config-geodata-*.json"))
	if err != nil || len(privateConfigs) != 0 {
		t.Fatalf("private configs leaked after core stop: %v, %v", privateConfigs, err)
	}
	record, err = scheduler.RunNow()
	if err != nil || record.Status != "skipped" || svc.IsXrayRunning() {
		t.Fatalf("manual stop bypassed: %+v, %v", record, err)
	}
}

func waitForMaintenanceCore(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !(&XrayService{}).IsXrayRunning() {
			t.Fatalf("core exited: %s", (&XrayService{}).GetXrayResult())
		}
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("core API did not become ready")
}
