package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

const (
	restartScheduleKey = "xrayRestartSchedule"
	restartHistoryKey  = "xrayRestartHistory"
	maxRestartHistory  = 50
)

// XrayRestartSchedule is one administrator-defined local-core restart plan.
type XrayRestartSchedule struct {
	Enabled  bool   `json:"enabled"`
	Cron     string `json:"cron"`
	Timezone string `json:"timezone"`
}

// XrayRestartRun records an actual attempt or a deliberate manual-stop skip.
type XrayRestartRun struct {
	StartedAt  int64  `json:"startedAt"`
	DurationMs int64  `json:"durationMs"`
	Trigger    string `json:"trigger"`
	Status     string `json:"status"`
	Error      string `json:"error"`
}

type XrayRestartScheduleView struct {
	Config  XrayRestartSchedule `json:"config"`
	NextRun int64               `json:"nextRun"`
	Running bool                `json:"running"`
	History []XrayRestartRun    `json:"history"`
}

// ParseMaintenanceSchedule isolates five-field plans from the seconds-enabled
// panel scheduler. Only restart plans accept fixed intervals.
func ParseMaintenanceSchedule(spec, timezone string, allowInterval bool) (cron.Schedule, error) {
	if timezone == "" || strings.TrimSpace(timezone) != timezone {
		return nil, errors.New("a valid IANA time zone is required")
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid time zone: %w", err)
	}
	spec = strings.TrimSpace(spec)
	if allowInterval && strings.HasPrefix(spec, "@every ") {
		duration, err := time.ParseDuration(strings.TrimPrefix(spec, "@every "))
		if err != nil || duration < time.Minute {
			return nil, errors.New("restart interval must be at least one minute")
		}
		return cron.Every(duration), nil
	}
	if len(strings.Fields(spec)) != 5 || strings.Contains(spec, "=") {
		return nil, errors.New("cron must contain five fields: minute hour day month weekday")
	}
	schedule, err := cron.ParseStandard("CRON_TZ=" + timezone + " " + spec)
	if err != nil {
		return nil, fmt.Errorf("invalid cron: %w", err)
	}
	if schedule.Next(time.Now().In(loc)).IsZero() {
		return nil, errors.New("cron has no upcoming execution")
	}
	return schedule, nil
}

// XrayScheduler restores persisted plans without replaying missed ticks.
// runMu serializes manual runs and replaced entries across cron job wrappers.
type XrayScheduler struct {
	ctx        context.Context
	cron       *cron.Cron
	settings   SettingService
	mu         sync.Mutex
	runMu      sync.Mutex
	entry      cron.EntryID
	generation uint64
	config     XrayRestartSchedule
	running    bool
	restart    func() (bool, error)
}

func NewXrayScheduler(ctx context.Context, c *cron.Cron) *XrayScheduler {
	return &XrayScheduler{
		ctx: ctx, cron: c,
		config:  XrayRestartSchedule{Cron: "0 4 * * 0", Timezone: "UTC"},
		restart: (&XrayService{}).RestartXrayScheduled,
	}
}

func (s *XrayScheduler) Restore() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.settings.getSetting(restartScheduleKey)
	if err != nil && !database.IsNotFound(err) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal([]byte(stored.Value), &s.config); err != nil {
			return err
		}
	}
	schedule, err := ParseMaintenanceSchedule(s.config.Cron, s.config.Timezone, true)
	if err != nil {
		return err
	}
	s.replaceLocked(s.config, schedule)
	return nil
}

func (s *XrayScheduler) Save(cfg XrayRestartSchedule) error {
	cfg.Cron = strings.TrimSpace(cfg.Cron)
	schedule, err := ParseMaintenanceSchedule(cfg.Cron, cfg.Timezone, true)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return err
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := s.settings.saveSetting(restartScheduleKey, string(encoded)); err != nil {
		return err
	}
	s.replaceLocked(cfg, schedule)
	return nil
}

func (s *XrayScheduler) replaceLocked(cfg XrayRestartSchedule, schedule cron.Schedule) {
	if s.entry != 0 {
		s.cron.Remove(s.entry)
		s.entry = 0
	}
	s.config = cfg
	s.generation++
	if cfg.Enabled {
		generation := s.generation
		s.entry = s.cron.Schedule(schedule, cron.FuncJob(func() {
			if _, err := s.run("scheduled", generation); err != nil {
				logger.Warning("scheduled Xray restart: ", err)
			}
		}))
	}
}

func (s *XrayScheduler) View() (*XrayRestartScheduleView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	history, err := s.historyLocked()
	if err != nil {
		return nil, err
	}
	view := &XrayRestartScheduleView{Config: s.config, Running: s.running, History: history}
	if s.entry != 0 {
		next := s.cron.Entry(s.entry).Next
		if !next.IsZero() {
			view.NextRun = next.UnixMilli()
		}
	}
	return view, nil
}

func (s *XrayScheduler) RunNow() (*XrayRestartRun, error) {
	return s.run("manual", 0)
}

func (s *XrayScheduler) run(trigger string, generation uint64) (*XrayRestartRun, error) {
	if !s.runMu.TryLock() {
		return nil, errors.New("an Xray restart task is already running")
	}
	defer s.runMu.Unlock()
	s.mu.Lock()
	if err := s.ctx.Err(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if trigger == "scheduled" && (!s.config.Enabled || generation != s.generation) {
		s.mu.Unlock()
		return nil, nil
	}
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	start := time.Now()
	record := &XrayRestartRun{StartedAt: start.UnixMilli(), Trigger: trigger, Status: "success"}
	attempted, runErr := s.restart()
	record.DurationMs = time.Since(start).Milliseconds()
	if runErr != nil {
		record.Status, record.Error = "failed", runErr.Error()
	} else if !attempted {
		record.Status = "skipped"
	}
	s.mu.Lock()
	history, persistErr := s.historyLocked()
	if persistErr == nil {
		history = append([]XrayRestartRun{*record}, history...)
		if len(history) > maxRestartHistory {
			history = history[:maxRestartHistory]
		}
		encoded, err := json.Marshal(history)
		persistErr = err
		if err == nil {
			persistErr = s.settings.saveSetting(restartHistoryKey, string(encoded))
		}
	}
	s.mu.Unlock()
	if persistErr != nil {
		persistErr = fmt.Errorf("restart result could not be saved: %w", persistErr)
	}
	return record, errors.Join(runErr, persistErr)
}

func (s *XrayScheduler) historyLocked() ([]XrayRestartRun, error) {
	history := []XrayRestartRun{}
	stored, err := s.settings.getSetting(restartHistoryKey)
	if database.IsNotFound(err) {
		return history, nil
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(stored.Value), &history)
	return history, err
}
