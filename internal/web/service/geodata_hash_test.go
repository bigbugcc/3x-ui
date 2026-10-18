package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestGeoHashReadFailureKeepsExecutionSuccessfulAndPreservesBaseline(t *testing.T) {
	setupSettingTestDB(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "geosite.dat")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := geodataHashFile(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	first := TaskExecutionLog{ID: "first", Task: "geodata", XrayRestartRun: XrayRestartRun{StartedAt: time.Now().UnixMilli(), Status: "success"}}
	if _, err := saveGeodataHashes(&first, []geodataHashSample{{file: "geosite.dat", path: path, hash: digest}}, 1000, "UTC"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	digest, readErr := geodataHashFile(path, time.Time{})
	if !os.IsNotExist(readErr) {
		t.Fatalf("missing-file snapshot error: %v", readErr)
	}
	next := TaskExecutionLog{ID: "second", Task: "geodata", XrayRestartRun: XrayRestartRun{StartedAt: time.Now().Add(time.Second).UnixMilli(), Status: "success"}}
	if _, err := saveGeodataHashes(&next, []geodataHashSample{{file: "geosite.dat", path: path, hash: digest, err: readErr}}, 2000, "UTC"); err != nil {
		t.Fatal(err)
	}
	logs, err := GetTaskExecutionLogs()
	if err != nil {
		t.Fatal(err)
	}
	if logs.History[0].Status != "success" || logs.History[0].HashComparisons[0].Status != "unavailable" || logs.History[0].HashComparisons[0].CurrentHash != "" {
		t.Fatalf("hash I/O failure changed execution result: %+v", logs.History[0])
	}
	baselines, err := geodataHashBaselinesLocked()
	if err != nil {
		t.Fatal(err)
	}
	if baseline := baselines[geodataHashBaselinePath(path)]; baseline.RunID != "first" || baseline.ConfirmedAt != 1000 {
		t.Fatalf("I/O failure advanced baseline: %+v", baseline)
	}
}

func TestGeoHashStateTransactionFailureCannotAdvanceBaselineOrRewriteLog(t *testing.T) {
	setupSettingTestDB(t)
	record := TaskExecutionLog{ID: "atomic", Task: "geodata", XrayRestartRun: XrayRestartRun{StartedAt: time.Now().UnixMilli(), Status: "success"}}
	if err := appendTaskExecutionLog(record); err != nil {
		t.Fatal(err)
	}
	db := database.GetDB()
	if err := db.Exec(`CREATE TRIGGER reject_geo_hash_baseline BEFORE INSERT ON settings WHEN NEW.key = 'geodataHashBaselines' BEGIN SELECT RAISE(ABORT, 'hash baseline rejected'); END`).Error; err != nil {
		t.Fatal(err)
	}
	_, err := saveGeodataHashes(&record, []geodataHashSample{{file: "geosite.dat", path: filepath.Join(t.TempDir(), "geosite.dat"), hash: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}}, 1000, "UTC")
	if err == nil || err.Error() != "hash baseline rejected" {
		t.Fatalf("unexpected transaction result: %v", err)
	}
	logs, err := GetTaskExecutionLogs()
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.History) != 1 || len(logs.History[0].HashComparisons) != 0 {
		t.Fatalf("failed baseline write partially changed log: %+v", logs)
	}
	var count int64
	if err := db.Model(&model.Setting{}).Where("key = ?", geodataHashBaselinesKey).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("baseline persisted despite rollback")
	}
}
