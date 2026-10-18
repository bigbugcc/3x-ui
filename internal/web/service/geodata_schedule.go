package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const geodataScheduleDraftKey = "geodataScheduleDraft"

// GeodataScheduleError keeps a localized explanation separate from technical evidence.
type GeodataScheduleError struct {
	Key  string
	File string
	Err  error
}

func (e *GeodataScheduleError) Error() string { return e.Err.Error() }
func (e *GeodataScheduleError) Unwrap() error { return e.Err }

type GeodataUpdateSchedule struct {
	Enabled  bool            `json:"enabled"`
	Cron     string          `json:"cron"`
	Timezone string          `json:"timezone"`
	Outbound string          `json:"outbound"`
	Assets   []GeodataSource `json:"assets"`
}

type GeodataScheduleView struct {
	Config          GeodataUpdateSchedule `json:"config"`
	NextRun         int64                 `json:"nextRun"`
	DisplayTimezone string                `json:"displayTimezone,omitempty"`
	StandardSources []GeodataSource       `json:"standardSources"`
	OutboundTags    []string              `json:"outboundTags"`
	Applied         bool                  `json:"applied"`
	ApplyError      string                `json:"applyError"`
}

func (s *XraySettingService) GetGeodataSchedule() (*GeodataScheduleView, error) {
	xrayTemplateMu.Lock()
	defer xrayTemplateMu.Unlock()
	return s.geodataScheduleViewLocked()
}

func (s *XraySettingService) geodataScheduleViewLocked() (*GeodataScheduleView, error) {
	template, err := s.GetXrayConfigTemplate()
	if err != nil {
		return nil, err
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(UnwrapXrayTemplateConfig(template)), &cfg); err != nil {
		return nil, err
	}
	plan := GeodataUpdateSchedule{Cron: "0 4 * * *", Timezone: "UTC", Assets: []GeodataSource{}}
	// Advanced JSON stays authoritative; the draft retains disabled assets.
	if raw, ok := cfg["geodata"]; ok && string(raw) != "null" {
		var geo struct {
			Cron     string          `json:"cron"`
			Outbound string          `json:"outbound"`
			Assets   []GeodataSource `json:"assets"`
		}
		if err := json.Unmarshal(raw, &geo); err != nil {
			return nil, err
		}
		plan.Enabled = geo.Cron != ""
		plan.Outbound, plan.Assets = geo.Outbound, geo.Assets
		if plan.Enabled {
			plan.Cron, plan.Timezone = splitGeodataCron(geo.Cron)
		}
	} else {
		stored, err := s.getSetting(geodataScheduleDraftKey)
		if err != nil && !database.IsNotFound(err) {
			return nil, err
		}
		if err == nil {
			if err := json.Unmarshal([]byte(stored.Value), &plan); err != nil {
				return nil, err
			}
		}
		plan.Enabled = false
	}
	if plan.Assets == nil {
		plan.Assets = []GeodataSource{}
	}
	view := &GeodataScheduleView{Config: plan, StandardSources: StandardGeodataSources(), OutboundTags: []string{}}
	if plan.Enabled {
		if schedule, err := ParseMaintenanceSchedule(plan.Cron, plan.Timezone, false); err == nil {
			view.NextRun = schedule.Next(time.Now()).UnixMilli()
		} else {
			view.ApplyError = err.Error()
		}
	}
	view.DisplayTimezone = executionTimezone(plan.Timezone, time.UnixMilli(view.NextRun))
	var outbounds []struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(cfg["outbounds"], &outbounds); err != nil && len(cfg["outbounds"]) > 0 {
		return nil, err
	}
	seen := map[string]bool{}
	for _, out := range outbounds {
		if out.Tag != "" && out.Protocol != "blackhole" && !seen[out.Tag] {
			view.OutboundTags = append(view.OutboundTags, out.Tag)
			seen[out.Tag] = true
		}
	}
	subscriptions := &OutboundSubscriptionService{}
	if tags, err := subscriptions.AllActiveOutboundTags(); err == nil {
		for _, tag := range tags {
			if !seen[tag] {
				view.OutboundTags = append(view.OutboundTags, tag)
				seen[tag] = true
			}
		}
	}
	return view, nil
}

// Unprefixed core plans use the system location, independently of the panel.
func splitGeodataCron(spec string) (string, string) {
	fields := strings.Fields(spec)
	if len(fields) > 0 && (strings.HasPrefix(fields[0], "CRON_TZ=") || strings.HasPrefix(fields[0], "TZ=")) {
		_, zone, _ := strings.Cut(fields[0], "=")
		return strings.Join(fields[1:], " "), zone
	}
	return strings.TrimSpace(spec), "Local"
}

func validateGeodataSchedule(plan GeodataUpdateSchedule) error {
	if _, err := ParseMaintenanceSchedule(plan.Cron, plan.Timezone, false); err != nil {
		return &GeodataScheduleError{Key: "geoInvalidSchedule", Err: err}
	}
	if plan.Enabled && len(plan.Assets) == 0 {
		return &GeodataScheduleError{Key: "geoReloadOnlyHint", Err: errors.New("at least one geodata download file is required before enabling updates")}
	}
	if len(plan.Assets) > 64 {
		return &GeodataScheduleError{Key: "geoTooManyFiles", Err: errors.New("at most 64 geodata files may be scheduled")}
	}
	seen := map[string]bool{}
	files := &ServerService{}
	for _, asset := range plan.Assets {
		uri, err := url.ParseRequestURI(asset.URL)
		if err != nil || uri.Scheme != "https" || uri.Host == "" || uri.User != nil || len(asset.URL) > 4096 {
			return &GeodataScheduleError{Key: "geoInvalidUrl", File: asset.File, Err: errors.New("geodata download URL must be HTTPS without embedded credentials")}
		}
		if !files.IsValidGeofileName(asset.File) || len(asset.File) > 255 || seen[asset.File] {
			return &GeodataScheduleError{Key: "geoInvalidFile", File: asset.File, Err: fmt.Errorf("invalid or duplicate geodata file: %q", asset.File)}
		}
		seen[asset.File] = true
		if plan.Enabled {
			info, err := os.Lstat(filepath.Join(config.GetBinFolderPath(), asset.File))
			if errors.Is(err, os.ErrNotExist) {
				entry, standard := geofileAllowlist[asset.File]
				if standard && asset.URL == entry.latestURL() {
					continue
				}
			}
			if err != nil || !info.Mode().IsRegular() {
				return &GeodataScheduleError{Key: "geoFileRequired", File: asset.File, Err: fmt.Errorf("geodata file %q must already exist in the asset folder before enabling updates", asset.File)}
			}
		}
	}
	return nil
}

// Only preset URLs may bootstrap missing files. Stage and verify the complete
// batch first; linking into place never replaces an existing administrator file.
func initializeGeodataScheduleAssets(ctx context.Context, plan GeodataUpdateSchedule) error {
	binFolder := config.GetBinFolderPath()
	byRepo := map[string][]geofileEntry{}
	for _, asset := range plan.Assets {
		if _, err := os.Lstat(filepath.Join(binFolder, asset.File)); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		entry, ok := geofileAllowlist[asset.File]
		if !ok || asset.URL != entry.latestURL() {
			return &GeodataScheduleError{Key: "geoFileRequired", File: asset.File, Err: fmt.Errorf("missing custom geodata file %q", asset.File)}
		}
		byRepo[entry.Repo] = append(byRepo[entry.Repo], entry)
	}
	if len(byRepo) == 0 {
		return nil
	}
	if err := os.MkdirAll(binFolder, 0755); err != nil {
		return &GeodataScheduleError{Key: "geoInitFailed", Err: err}
	}
	stageDir, err := os.MkdirTemp(binFolder, "geofile-init-")
	if err != nil {
		return &GeodataScheduleError{Key: "geoInitFailed", Err: err}
	}
	defer os.RemoveAll(stageDir)
	server := &ServerService{}
	client := server.settingService.NewProxiedHTTPClient(60 * time.Second)
	var staged []stagedGeofile
	for _, repo := range slices.Sorted(maps.Keys(byRepo)) {
		entries := byRepo[repo]
		slices.SortFunc(entries, func(a, b geofileEntry) int { return strings.Compare(a.FileName, b.FileName) })
		batch, err := server.stageGeofileRelease(ctx, client, entries, binFolder, stageDir)
		if err != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			return &GeodataScheduleError{Key: "geoInitFailed", Err: err}
		}
		staged = append(staged, batch...)
	}
	for _, file := range staged {
		if err := ctx.Err(); err != nil {
			return &GeodataScheduleError{Key: "geoInitFailed", Err: err}
		}
		if err := os.Link(file.stagePath, file.destPath); err != nil && !errors.Is(err, os.ErrExist) {
			return &GeodataScheduleError{Key: "geoInitFailed", Err: err}
		}
	}
	// Includes unexpected 304 responses and files removed/replaced during download.
	for _, asset := range plan.Assets {
		info, err := os.Lstat(filepath.Join(binFolder, asset.File))
		if err != nil || !info.Mode().IsRegular() {
			return &GeodataScheduleError{Key: "geoFileRequired", File: asset.File, Err: fmt.Errorf("geodata file %q is not a regular file after initialization", asset.File)}
		}
	}
	return nil
}

func validateGeodataOutbound(outbound string, view *GeodataScheduleView) error {
	if outbound != "" && !slices.Contains(view.OutboundTags, outbound) {
		return &GeodataScheduleError{Key: "geoOutboundUnavailable", Err: errors.New("geodata download outbound is not available")}
	}
	return nil
}

// SaveGeodataSchedule atomically patches the latest template and its draft,
// preserving unrelated changes made since the browser loaded its plan.
func (s *XraySettingService) SaveGeodataSchedule(ctx context.Context, plan GeodataUpdateSchedule) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	plan.Cron = strings.TrimSpace(plan.Cron)
	if plan.Assets == nil {
		plan.Assets = []GeodataSource{}
	}
	if err := validateGeodataSchedule(plan); err != nil {
		return err
	}
	if plan.Enabled {
		view, err := s.GetGeodataSchedule()
		if err != nil {
			return err
		}
		if err := validateGeodataOutbound(plan.Outbound, view); err != nil {
			return err
		}
		// Network I/O must not block unrelated template reads and writes.
		if err := initializeGeodataScheduleAssets(ctx, plan); err != nil {
			return err
		}
	}
	xrayTemplateMu.Lock()
	defer xrayTemplateMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	template, err := s.GetXrayConfigTemplate()
	if err != nil {
		return err
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(UnwrapXrayTemplateConfig(template)), &cfg); err != nil {
		return err
	}
	if cfg == nil {
		return errors.New("xray template must be a JSON object")
	}
	if plan.Enabled {
		if plan.Outbound != "" {
			view, err := s.geodataScheduleViewLocked()
			if err != nil {
				return err
			}
			if err := validateGeodataOutbound(plan.Outbound, view); err != nil {
				return err
			}
		}
		geo := struct {
			Cron     string          `json:"cron"`
			Outbound string          `json:"outbound,omitempty"`
			Assets   []GeodataSource `json:"assets"`
		}{"CRON_TZ=" + plan.Timezone + " " + plan.Cron, plan.Outbound, plan.Assets}
		raw, err := json.Marshal(geo)
		if err != nil {
			return err
		}
		cfg["geodata"] = raw
	} else {
		delete(cfg, "geodata")
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	draft, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	return database.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for key, value := range map[string]string{"xrayTemplateConfig": string(encoded), geodataScheduleDraftKey: string(draft)} {
			var setting model.Setting
			err := tx.Where("key = ?", key).First(&setting).Error
			if database.IsNotFound(err) {
				setting = model.Setting{Key: key, Value: value}
				if err := tx.Create(&setting).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else {
				setting.Value = value
				if err := tx.Save(&setting).Error; err != nil {
					return err
				}
			}
		}
		return ctx.Err()
	})
}
