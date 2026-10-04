package service

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	if err := os.Symlink(binary, filepath.Join(dir, xray.GetBinaryName())); err != nil {
		t.Fatal(err)
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
	record, err := scheduler.RunNow()
	if err != nil || record.Status != "success" {
		t.Fatalf("real restart = %+v, %v", record, err)
	}
	waitForMaintenanceCore(t, port)
	second := currentXrayProcess()
	if second == first || first.IsRunning() {
		t.Fatal("scheduled task did not replace the running core")
	}
	plan := GeodataUpdateSchedule{Cron: "0 4 * * *", Timezone: "Asia/Shanghai", Assets: []GeodataSource{}}
	if err := settings.SaveGeodataSchedule(plan); err != nil {
		t.Fatal(err)
	}
	if applied, err := svc.RestartXrayIfRunning(); !applied || err != nil {
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
	if err := settings.SaveGeodataSchedule(plan); err != nil {
		t.Fatal(err)
	}
	if applied, err := svc.RestartXrayIfRunning(); !applied || err != nil {
		t.Fatalf("Geo apply = %v, %v", applied, err)
	}
	waitForMaintenanceCore(t, port)
	if currentXrayProcess() == second || second.IsRunning() {
		t.Fatal("changed Geo plan did not restart the core")
	}
	if err := svc.StopXray(); err != nil {
		t.Fatal(err)
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
