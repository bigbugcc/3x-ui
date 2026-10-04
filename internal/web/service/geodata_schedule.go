package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const geodataScheduleDraftKey = "geodataScheduleDraft"

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
		return err
	}
	if len(plan.Assets) > 64 {
		return errors.New("at most 64 geodata files may be scheduled")
	}
	seen := map[string]bool{}
	files := &ServerService{}
	for _, asset := range plan.Assets {
		uri, err := url.ParseRequestURI(asset.URL)
		if err != nil || uri.Scheme != "https" || uri.Host == "" || uri.User != nil || len(asset.URL) > 4096 {
			return errors.New("geodata download URL must be HTTPS without embedded credentials")
		}
		if !files.IsValidGeofileName(asset.File) || len(asset.File) > 255 || seen[asset.File] {
			return fmt.Errorf("invalid or duplicate geodata file: %q", asset.File)
		}
		seen[asset.File] = true
		if plan.Enabled {
			info, err := os.Stat(filepath.Join(config.GetBinFolderPath(), asset.File))
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("geodata file %q must already exist in the asset folder before enabling updates", asset.File)
			}
		}
	}
	return nil
}

// SaveGeodataSchedule atomically patches the latest template and its draft,
// preserving unrelated changes made since the browser loaded its plan.
func (s *XraySettingService) SaveGeodataSchedule(plan GeodataUpdateSchedule) error {
	plan.Cron = strings.TrimSpace(plan.Cron)
	if plan.Assets == nil {
		plan.Assets = []GeodataSource{}
	}
	if err := validateGeodataSchedule(plan); err != nil {
		return err
	}
	xrayTemplateMu.Lock()
	defer xrayTemplateMu.Unlock()
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
			found := false
			for _, tag := range view.OutboundTags {
				found = found || tag == plan.Outbound
			}
			if !found {
				return errors.New("geodata download outbound is not available")
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
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
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
		return nil
	})
}
