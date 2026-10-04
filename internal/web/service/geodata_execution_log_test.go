package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/util/json_util"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

type monitoredGeoProcess struct {
	running bool
	cfg     *xray.Config
	events  []xray.GeodataEvent
}

func (p *monitoredGeoProcess) IsRunning() bool         { return p.running }
func (p *monitoredGeoProcess) GetConfig() *xray.Config { return p.cfg }
func (p *monitoredGeoProcess) GeodataEvents(after uint64) []xray.GeodataEvent {
	var events []xray.GeodataEvent
	for _, event := range p.events {
		if event.Sequence > after {
			events = append(events, event)
		}
	}
	return events
}
func (p *monitoredGeoProcess) emit(kind string, at time.Time) {
	p.events = append(p.events, xray.GeodataEvent{Sequence: uint64(len(p.events) + 1), Kind: kind, At: at, Message: kind})
}

func TestGeoExecutionLogsConfirmNativeReloadWithoutAssets(t *testing.T) {
	setupSettingTestDB(t)
	p := &monitoredGeoProcess{running: true, cfg: &xray.Config{Geodata: json_util.RawMessage(`{"cron":"* * * * *","assets":[]}`)}}
	m := &GeodataExecutionMonitor{}
	start := time.Now()
	// A scheduled timestamp alone must never invent an execution.
	m.observe(p, start)
	m.observe(p, start.Add(time.Minute))
	logs, err := GetTaskExecutionLogs()
	if err != nil || len(logs.History) != 0 {
		t.Fatalf("invented tick: %+v %v", logs, err)
	}
	p.emit("ip-start", start)
	p.emit("ip-ok", start.Add(time.Millisecond))
	p.emit("site-start", start.Add(time.Millisecond))
	m.observe(p, start.Add(time.Second))
	logs, _ = GetTaskExecutionLogs()
	if len(logs.History) != 1 || logs.History[0].Status != "running" {
		t.Fatalf("incomplete reload: %+v", logs)
	}
	p.emit("site-ok", start.Add(2*time.Millisecond))
	m.observe(p, start.Add(2*time.Second))
	logs, _ = GetTaskExecutionLogs()
	if len(logs.History) != 1 || logs.History[0].Status != "success" || logs.History[0].Details != "geoReloadOnly" || logs.History[0].DurationMs != 2 {
		t.Fatalf("completed reload: %+v", logs)
	}
	p.running = false
	m.observe(p, start.Add(3*time.Second))
	logs, _ = GetTaskExecutionLogs()
	if logs.History[0].Status != "success" {
		t.Fatalf("stop erased success: %+v", logs)
	}
}

func TestGeoExecutionLogsWaitForCommitAndOverrideLateFailure(t *testing.T) {
	setupSettingTestDB(t)
	dir := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", t.TempDir())
	t.Setenv("XRAY_LOCATION_ASSET", t.TempDir())
	path := filepath.Join(dir, "geosite.dat")
	backup := filepath.Join(dir, ".geosite.dat.test.bak")
	if err := os.WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &monitoredGeoProcess{running: true, cfg: &xray.Config{Geodata: json_util.RawMessage(`{"cron":"* * * * *","assets":[{"file":"geosite.dat"}]}`)}}
	start := time.Now()
	p.emit("download", start)
	p.events[0].Target = path
	p.emit("ip-start", start.Add(time.Millisecond))
	p.emit("ip-ok", start.Add(2*time.Millisecond))
	p.emit("site-start", start.Add(2*time.Millisecond))
	p.emit("site-ok", start.Add(3*time.Millisecond))
	m := &GeodataExecutionMonitor{}
	m.observe(p, start.Add(time.Second))
	logs, _ := GetTaskExecutionLogs()
	if len(logs.History) != 1 || logs.History[0].Status != "running" {
		t.Fatalf("uncommitted reload: %+v", logs)
	}
	if err := os.Remove(backup); err != nil {
		t.Fatal(err)
	}
	m.observe(p, start.Add(2*time.Second))
	logs, _ = GetTaskExecutionLogs()
	if logs.History[0].Status != "success" || logs.History[0].Details != "geoFilesUpdated" {
		t.Fatalf("commit: %+v", logs)
	}
	p.emit("failed", start.Add(4*time.Millisecond))
	m.observe(p, start.Add(3*time.Second))
	logs, _ = GetTaskExecutionLogs()
	if len(logs.History) != 1 || logs.History[0].Status != "failed" {
		t.Fatalf("late commit failure: %+v", logs)
	}
	// Both reloads are evaluated even if GeoIP reload fails; GeoSite alone proves nothing.
	p.emit("download", start.Add(4*time.Second))
	p.emit("ip-start", start.Add(4*time.Second))
	p.emit("site-start", start.Add(4*time.Second))
	p.emit("site-ok", start.Add(4*time.Second))
	p.emit("failed", start.Add(4*time.Second))
	m.observe(p, start.Add(5*time.Second))
	logs, _ = GetTaskExecutionLogs()
	if len(logs.History) != 2 || logs.History[0].Status != "failed" {
		t.Fatalf("partial reload incorrectly succeeded: %+v", logs)
	}
}

func TestGeoExecutionLogsDrainMultipleRunsAndKeepInterruptedResultsUnknown(t *testing.T) {
	setupSettingTestDB(t)
	p := &monitoredGeoProcess{running: true, cfg: &xray.Config{Geodata: json_util.RawMessage(`{"cron":"* * * * *","assets":[]}`)}}
	start := time.Now()
	for i := range 2 {
		at := start.Add(time.Duration(i) * time.Second)
		p.emit("ip-start", at)
		p.emit("ip-ok", at)
		p.emit("site-start", at)
		p.emit("site-ok", at.Add(time.Millisecond))
	}
	m := &GeodataExecutionMonitor{}
	m.observe(p, start.Add(3*time.Second))
	logs, _ := GetTaskExecutionLogs()
	if len(logs.History) != 2 || logs.History[0].Status != "success" || logs.History[1].Status != "success" {
		t.Fatalf("coalesced runs: %+v", logs)
	}
	p.emit("ip-start", start.Add(4*time.Second))
	m.observe(p, start.Add(4*time.Second))
	p.running = false
	m.observe(p, start.Add(5*time.Second))
	logs, _ = GetTaskExecutionLogs()
	if len(logs.History) != 3 || logs.History[0].Status != "unconfirmed" || logs.History[0].Details != "geoCoreStopped" {
		t.Fatalf("interrupted run: %+v", logs)
	}
}

func TestGeoHashesCompareLastSuccessfulFilesAndSurviveClearingLogs(t *testing.T) {
	setupSettingTestDB(t)
	dir := t.TempDir()
	t.Setenv("XRAY_LOCATION_ASSET", dir)
	path := filepath.Join(dir, "geosite.dat")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	const abcHash = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	p := &monitoredGeoProcess{running: true, cfg: &xray.Config{Geodata: json_util.RawMessage(`{"cron":"* * * * *","assets":[{"file":"geosite.dat"}]}`)}}
	m := &GeodataExecutionMonitor{}
	start := time.Now()
	run := func(at time.Time) TaskExecutionLog {
		t.Helper()
		p.emit("download", at)
		p.events[len(p.events)-1].Target = path
		p.emit("ip-start", at)
		p.emit("ip-ok", at)
		p.emit("site-start", at)
		p.emit("site-ok", at.Add(time.Millisecond))
		m.observe(p, at.Add(time.Second))
		logs, err := GetTaskExecutionLogs()
		if err != nil {
			t.Fatal(err)
		}
		if len(logs.History) == 0 {
			t.Fatal("no execution record")
		}
		return logs.History[0]
	}
	first := run(start)
	if first.Status != "success" || len(first.HashComparisons) != 1 || first.HashComparisons[0].Status != "baseline" || first.HashComparisons[0].CurrentHash != abcHash || first.HashComparisons[0].PreviousHash != "" {
		t.Fatalf("initial baseline: %+v", first)
	}
	second := run(start.Add(2 * time.Second))
	if second.HashComparisons[0].Status != "unchanged" || second.HashComparisons[0].PreviousHash != abcHash || second.HashComparisons[0].BaselineAt != start.Add(time.Millisecond).UnixMilli() {
		t.Fatalf("same contents must still succeed: %+v", second)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	third := run(start.Add(4 * time.Second))
	if third.HashComparisons[0].Status != "changed" || third.HashComparisons[0].PreviousHash != abcHash || third.HashComparisons[0].CurrentHash != emptyHash {
		t.Fatalf("changed contents: %+v", third)
	}
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = run(start.Add(6 * time.Second))
	p.emit("failed", start.Add(6*time.Second+2*time.Millisecond))
	m.observe(p, start.Add(7*time.Second))
	logs, _ := GetTaskExecutionLogs()
	if logs.History[0].Status != "failed" || len(logs.History[0].HashComparisons) != 0 {
		t.Fatalf("late failure retained a successful comparison: %+v", logs.History[0])
	}
	fifth := run(start.Add(8 * time.Second))
	if fifth.HashComparisons[0].Status != "changed" || fifth.HashComparisons[0].PreviousHash != emptyHash {
		t.Fatalf("failed execution advanced the baseline: %+v", fifth)
	}
	if err := ClearTaskExecutionLogs(); err != nil {
		t.Fatal(err)
	}
	m = NewGeodataExecutionMonitor()
	p = &monitoredGeoProcess{running: true, cfg: p.cfg}
	sixth := run(start.Add(10 * time.Second))
	if sixth.HashComparisons[0].Status != "unchanged" || sixth.HashComparisons[0].PreviousHash != abcHash {
		t.Fatalf("clearing history or replacing monitor erased the baseline: %+v", sixth)
	}
}

func TestGeoHashSnapshotDoesNotPromoteFilesChangedAfterTheConfirmedRun(t *testing.T) {
	setupSettingTestDB(t)
	dir := t.TempDir()
	t.Setenv("XRAY_LOCATION_ASSET", dir)
	path := filepath.Join(dir, "geosite.dat")
	completed := time.Now().Add(-time.Minute)
	if err := os.WriteFile(path, []byte("later contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &monitoredGeoProcess{running: true, cfg: &xray.Config{Geodata: json_util.RawMessage(`{"cron":"* * * * *","assets":[{"file":"geosite.dat"}]}`)}}
	p.emit("download", completed.Add(-time.Second))
	p.events[0].Target = path
	p.emit("ip-start", completed.Add(-time.Millisecond))
	p.emit("ip-ok", completed.Add(-time.Millisecond))
	p.emit("site-start", completed.Add(-time.Millisecond))
	p.emit("site-ok", completed)
	m := &GeodataExecutionMonitor{}
	m.observe(p, time.Now())
	logs, err := GetTaskExecutionLogs()
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.History) != 1 || logs.History[0].Status != "success" || len(logs.History[0].HashComparisons) != 1 || logs.History[0].HashComparisons[0].Status != "unavailable" || logs.History[0].HashComparisons[0].CurrentHash != "" {
		t.Fatalf("later contents were attributed to the completed run: %+v", logs)
	}
	baselines, err := geodataHashBaselinesLocked()
	if err != nil {
		t.Fatal(err)
	}
	if len(baselines) != 0 {
		t.Fatalf("unconfirmed file contents were promoted: %+v", baselines)
	}
}

func TestGeoHashRollbackRetriesBeforeProcessingNextQueuedRun(t *testing.T) {
	setupSettingTestDB(t)
	path := filepath.Join(t.TempDir(), "geosite.dat")
	const abcHash = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	p := &monitoredGeoProcess{running: true, cfg: &xray.Config{Geodata: json_util.RawMessage(`{"cron":"* * * * *","assets":[{"file":"geosite.dat"}]}`)}}
	m := &GeodataExecutionMonitor{}
	start := time.Now()
	run := func(contents string, at time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		p.emit("download", at)
		p.events[len(p.events)-1].Target = path
		p.emit("ip-start", at)
		p.emit("ip-ok", at)
		p.emit("site-start", at)
		p.emit("site-ok", at.Add(time.Millisecond))
		m.observe(p, at.Add(time.Second))
	}
	run("abc", start)
	run("", start.Add(2*time.Second))
	db := database.GetDB()
	if err := db.Exec(`CREATE TRIGGER reject_geo_hash_rollback BEFORE UPDATE ON settings WHEN NEW.key = 'geodataHashBaselines' BEGIN SELECT RAISE(ABORT, 'temporary hash rollback failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	p.emit("failed", start.Add(2*time.Second+2*time.Millisecond))
	run("abc", start.Add(5*time.Second))
	if err := db.Exec(`DROP TRIGGER reject_geo_hash_rollback`).Error; err != nil {
		t.Fatal(err)
	}
	m.observe(p, start.Add(7*time.Second))
	logs, err := GetTaskExecutionLogs()
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.History) != 3 || logs.History[1].Status != "failed" {
		t.Fatalf("failure recovery lost execution records: %+v", logs)
	}
	comparison := logs.History[0].HashComparisons[0]
	if comparison.Status != "unchanged" || comparison.PreviousHash != abcHash {
		t.Fatalf("failed rollback contaminated the next baseline: %+v", comparison)
	}
}
