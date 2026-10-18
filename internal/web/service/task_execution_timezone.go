package service

import (
	"encoding/json"
	"time"
)

// Resolve Local on the server at the displayed instant, including DST.
func executionTimezone(zone string, at time.Time) string {
	if zone == "Local" {
		return at.In(time.Local).Format("-07:00")
	}
	if zone == "" {
		return "UTC"
	}
	return zone
}

// Enrich responses only; current settings cannot reconstruct historical choices.
func inferTaskLogTimezones(history []TaskExecutionLog) {
	missing := false
	for _, record := range history {
		if record.Timezone == "" {
			missing = true
			break
		}
	}
	if !missing {
		return
	}
	zones := map[string]string{"restart": "UTC", "geodata": "UTC"}
	settings := &SettingService{}
	if stored, err := settings.getSetting(restartScheduleKey); err == nil {
		var plan XrayRestartSchedule
		if json.Unmarshal([]byte(stored.Value), &plan) == nil && plan.Timezone != "" {
			zones["restart"] = plan.Timezone
		}
	}
	if template, err := settings.GetXrayConfigTemplate(); err == nil {
		var cfg struct {
			Geodata *struct {
				Cron string `json:"cron"`
			} `json:"geodata"`
		}
		if json.Unmarshal([]byte(UnwrapXrayTemplateConfig(template)), &cfg) == nil && cfg.Geodata != nil {
			_, zones["geodata"] = splitGeodataCron(cfg.Geodata.Cron)
		} else if stored, err := settings.getSetting(geodataScheduleDraftKey); err == nil {
			var plan GeodataUpdateSchedule
			if json.Unmarshal([]byte(stored.Value), &plan) == nil && plan.Timezone != "" {
				zones["geodata"] = plan.Timezone
			}
		}
	}
	for i := range history {
		if history[i].Timezone != "" {
			continue
		}
		zone := zones[history[i].Task]
		history[i].Timezone = executionTimezone(zone, time.UnixMilli(history[i].StartedAt))
		history[i].TimezoneInferred = true
		for j := range history[i].HashComparisons {
			comparison := &history[i].HashComparisons[j]
			comparison.BaselineTimezone = executionTimezone(zone, time.UnixMilli(comparison.BaselineAt))
		}
	}
}
