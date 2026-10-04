package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// Recreate selected handlers after the core restart using their effective config.
// Keep the lifecycle lock so a concurrent manual stop cannot revive connections.
func (s *XrayService) RestartScheduledOutbounds(tags []string) ([]TaskExecutionStep, error) {
	lock.Lock()
	defer lock.Unlock()
	process := currentXrayProcess()
	if isManuallyStopped.Load() || process == nil || !process.IsRunning() {
		return nil, errors.New("Xray stopped before additional outbounds could reconnect")
	}
	var outbounds []json.RawMessage
	if err := json.Unmarshal(process.GetConfig().OutboundConfigs, &outbounds); err != nil {
		return nil, err
	}
	byTag := map[string]json.RawMessage{}
	for _, raw := range outbounds {
		var meta struct {
			Tag string `json:"tag"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, err
		}
		byTag[meta.Tag] = raw
	}
	api := &xray.XrayAPI{}
	if err := api.Init(process.GetAPIPort()); err != nil {
		return nil, err
	}
	defer api.Close()
	steps := make([]TaskExecutionStep, 0, len(tags))
	var runErrors []error
	for _, tag := range tags {
		step := TaskExecutionStep{Target: tag, Status: "success"}
		raw, ok := byTag[tag]
		var err error
		if !ok {
			err = fmt.Errorf("outbound %q is no longer available", tag)
		} else {
			// The child starts asynchronously; wait briefly for HandlerService.
			deadline := time.Now().Add(10 * time.Second)
			removed := false
			for {
				err = api.DelOutbound(tag)
				if err == nil || xray.IsMissingHandlerErr(err) {
					removed = true
					err = api.AddOutbound(raw)
					break
				}
				if !process.IsRunning() || time.Now().After(deadline) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			// Restore the handler if the first addition failed transiently.
			if removed && err != nil {
				err = errors.Join(err, addOutboundReconciling(api, raw))
			}
		}
		if err != nil {
			step.Status, step.Error = "failed", err.Error()
			runErrors = append(runErrors, fmt.Errorf("restart outbound %q: %w", tag, err))
		}
		steps = append(steps, step)
	}
	return steps, errors.Join(runErrors...)
}
