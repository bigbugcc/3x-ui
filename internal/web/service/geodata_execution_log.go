package service

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

type geodataLogProcess interface {
	IsRunning() bool
	GetConfig() *xray.Config
	GeodataEvents(uint64) []xray.GeodataEvent
}

type geodataHashRollback struct {
	record TaskExecutionLog
	undo   map[string]*geodataHashBaseline
}

// Observe actual native-core events without introducing a second update job.
type GeodataExecutionMonitor struct {
	mu        sync.Mutex
	process   geodataLogProcess
	sequence  uint64
	pending   *TaskExecutionLog
	ipOK      bool
	siteOK    bool
	completed time.Time
	targets   map[string]string
	hashUndo  map[string]*geodataHashBaseline
	rollback  *geodataHashRollback
	lastPrune time.Time
}

func NewGeodataExecutionMonitor() *GeodataExecutionMonitor {
	taskHistoryMu.Lock()
	defer taskHistoryMu.Unlock()
	history, err := taskHistoryLocked()
	if err == nil {
		for i := range history {
			if history[i].Task == "geodata" && history[i].Status == "running" {
				history[i].Status, history[i].Details = "unconfirmed", "geoCoreStopped"
				for j := range history[i].Steps {
					history[i].Steps[j].Status = "unconfirmed"
				}
			}
		}
		var cfg TaskLogSettings
		cfg, err = taskLogSettingsLocked()
		if err == nil {
			err = saveTaskHistoryLocked(history, cfg)
		}
	}
	if err != nil {
		logger.Warning("recover Geo execution records: ", err)
	}
	return &GeodataExecutionMonitor{}
}

func (m *GeodataExecutionMonitor) Poll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	var process geodataLogProcess
	if p := currentXrayProcess(); p != nil {
		process = p
	}
	m.observe(process, time.Now())
}

func (m *GeodataExecutionMonitor) persist(record TaskExecutionLog) {
	if err := appendTaskExecutionLog(record); err != nil {
		logger.Warning("save Geo execution record: ", err)
	}
}

func (m *GeodataExecutionMonitor) finish(status, details, runError string, at time.Time) {
	if m.pending == nil {
		return
	}
	m.pending.Status, m.pending.Details, m.pending.Error = status, details, runError
	for i := range m.pending.Steps {
		m.pending.Steps[i].Status = status
	}
	m.pending.DurationMs = max(int64(0), at.UnixMilli()-m.pending.StartedAt)
	if status == "failed" {
		m.pending.HashComparisons = nil
		if len(m.hashUndo) > 0 {
			if err := rollbackGeodataHashes(*m.pending, m.hashUndo); err != nil {
				logger.Warning("restore Geo hash baseline: ", err)
				m.rollback = &geodataHashRollback{record: *m.pending, undo: m.hashUndo}
			} else {
				m.hashUndo = nil
				return
			}
		}
	}
	m.persist(*m.pending)
}

func (m *GeodataExecutionMonitor) begin(at time.Time, assets []GeodataSource) {
	if m.pending != nil && m.pending.Status == "running" {
		m.finish("unconfirmed", "geoResultUnavailable", "", at)
	}
	m.pending = &TaskExecutionLog{ID: uuid.NewString(), XrayRestartRun: XrayRestartRun{StartedAt: at.UnixMilli(), Trigger: "scheduled", Status: "running"}, Task: "geodata"}
	for _, asset := range assets {
		m.pending.Steps = append(m.pending.Steps, TaskExecutionStep{Target: asset.File, Status: "running"})
	}
	m.ipOK, m.siteOK, m.completed = false, false, time.Time{}
	m.targets = make(map[string]string)
	m.hashUndo = nil
	m.persist(*m.pending)
}

func (m *GeodataExecutionMonitor) observe(process geodataLogProcess, now time.Time) {
	if m.rollback != nil {
		if err := rollbackGeodataHashes(m.rollback.record, m.rollback.undo); err != nil {
			logger.Warning("retry Geo hash baseline restoration: ", err)
			return
		}
		m.rollback, m.hashUndo = nil, nil
	}
	if m.lastPrune.IsZero() || now.Sub(m.lastPrune) >= time.Hour {
		if _, err := GetTaskExecutionLogs(); err != nil {
			logger.Warning("prune task execution records: ", err)
		}
		m.lastPrune = now
	}
	if process != m.process {
		if m.process != nil {
			// Drain final events before switching to the replacement core.
			m.observe(m.process, now)
			if m.rollback != nil {
				return
			}
		}
		if m.pending != nil && m.pending.Status == "running" {
			m.finish("unconfirmed", "geoCoreStopped", "", now)
		}
		m.process, m.sequence, m.pending = process, 0, nil
	}
	if process == nil {
		return
	}
	var geo struct {
		Assets []GeodataSource `json:"assets"`
	}
	if cfg := process.GetConfig(); cfg != nil {
		_ = json.Unmarshal(cfg.Geodata, &geo)
	}
	for _, event := range process.GeodataEvents(m.sequence) {
		if event.Sequence != m.sequence+1 {
			if m.pending != nil && m.pending.Status == "running" {
				m.finish("unconfirmed", "geoResultUnavailable", "", event.At)
			}
			m.pending = nil
		}
		m.sequence = event.Sequence
		// A new execution starts with a download, or GeoIP reload when no assets exist.
		starts := event.Kind == "download" || event.Kind == "ip-start"
		if starts && m.pending != nil && m.pending.Status == "running" && !m.completed.IsZero() {
			// The next native execution proves the preceding commit returned.
			if m.ipOK && m.siteOK {
				m.succeed(geo.Assets, false)
			}
			m.begin(event.At, geo.Assets)
		} else if (starts && (m.pending == nil || m.pending.Status != "running")) || (m.pending == nil && event.Kind == "failed") {
			m.begin(event.At, geo.Assets)
		}
		if m.pending == nil {
			continue
		}
		switch event.Kind {
		case "download":
			if event.Target != "" {
				m.targets[filepath.Base(event.Target)] = event.Target
			}
		case "ip-ok":
			m.ipOK = true
		case "site-ok":
			m.siteOK, m.completed = true, event.At
		case "failed":
			m.finish("failed", "", event.Message, event.At)
		}
		if m.rollback != nil {
			return
		}
	}
	if m.pending == nil || m.pending.Status != "running" {
		return
	}
	if m.ipOK && m.siteOK && geoCommitComplete(geo.Assets, m.targets) {
		m.succeed(geo.Assets, true)
	} else if !process.IsRunning() {
		m.finish("unconfirmed", "geoCoreStopped", "", now)
	} else if now.UnixMilli()-m.pending.StartedAt >= (15 * time.Minute).Milliseconds() {
		m.finish("unconfirmed", "geoResultUnavailable", "", now)
	}
}

func (m *GeodataExecutionMonitor) succeed(assets []GeodataSource, captureHashes bool) {
	details := "geoReloadOnly"
	if len(assets) > 0 {
		details = "geoFilesUpdated"
	}
	m.finish("success", details, "", m.completed)
	if len(assets) == 0 {
		return
	}
	samples := make([]geodataHashSample, 0, len(assets))
	for _, asset := range assets {
		path := geodataExecutionAssetPath(asset.File, m.targets)
		sample := geodataHashSample{file: asset.File, path: path}
		if captureHashes {
			sample.hash, sample.err = geodataHashFile(path, m.completed)
		} else {
			sample.err = errors.New("geoHashNextRun")
		}
		samples = append(samples, sample)
	}
	// Discard snapshots when queued events show a later execution or failure.
	for _, event := range m.process.GeodataEvents(m.sequence) {
		if event.Kind == "download" || event.Kind == "ip-start" || event.Kind == "failed" {
			for i := range samples {
				samples[i].hash, samples[i].err = "", errors.New("geoHashNextRun")
			}
			break
		}
	}
	undo, err := saveGeodataHashes(m.pending, samples, m.completed.UnixMilli())
	if err != nil {
		m.pending.HashComparisons = make([]GeodataHashComparison, 0, len(assets))
		for _, asset := range assets {
			m.pending.HashComparisons = append(m.pending.HashComparisons, GeodataHashComparison{File: asset.File, Status: "unavailable", Error: err.Error()})
		}
		logger.Warning("save Geo hash comparison: ", err)
		m.persist(*m.pending)
		return
	}
	m.hashUndo = undo
}

func geodataExecutionAssetPath(file string, targets map[string]string) string {
	if path := targets[file]; path != "" {
		return path
	}
	return filepath.Join(assetDir(), file)
}

func geoCommitComplete(assets []GeodataSource, targets map[string]string) bool {
	for _, asset := range assets {
		if !(&ServerService{}).IsValidGeofileName(asset.File) {
			return false
		}
		path := geodataExecutionAssetPath(asset.File, targets)
		if _, err := os.Stat(path); err != nil {
			return false
		}
		backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".*.bak"))
		if err != nil || len(backups) > 0 {
			return false
		}
	}
	return true
}
