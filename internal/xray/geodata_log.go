package xray

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// Capture native scheduler events before restoring the configured log filtering.
type geodataLogWriter struct {
	mu         sync.Mutex
	writer     *LogWriter
	buffer     string
	level      string
	path       string
	file       *os.File
	sinkFailed bool
}

var nativeLogSeverity = map[string]int{"none": -1, "error": 0, "warning": 1, "info": 2, "debug": 3}

func (lw *LogWriter) prepareGeodataLogging(data []byte) ([]byte, *geodataLogWriter, error) {
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, nil, err
	}
	var geo struct {
		Cron string `json:"cron"`
	}
	if json.Unmarshal(cfg["geodata"], &geo) != nil || geo.Cron == "" {
		return data, nil, nil
	}
	var logCfg map[string]json.RawMessage
	if len(cfg["log"]) > 0 && string(cfg["log"]) != "null" {
		if err := json.Unmarshal(cfg["log"], &logCfg); err != nil {
			return nil, nil, err
		}
	}
	if logCfg == nil {
		logCfg = map[string]json.RawMessage{"access": json.RawMessage(`"none"`)}
	}
	w := &geodataLogWriter{writer: lw}
	_ = json.Unmarshal(logCfg["loglevel"], &w.level)
	_ = json.Unmarshal(logCfg["error"], &w.path)
	w.level = strings.ToLower(w.level)
	if w.path != "" && w.path != "none" && w.level != "none" {
		file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, nil, err
		}
		w.file = file
	}
	if w.level != "debug" {
		logCfg["loglevel"] = json.RawMessage(`"info"`)
	}
	logCfg["error"] = json.RawMessage(`""`)
	if w.level == "none" {
		logCfg["access"] = json.RawMessage(`"none"`)
	}
	cfg["log"], _ = json.Marshal(logCfg)
	runtimeData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		_ = w.Close()
		return nil, nil, err
	}
	return runtimeData, w, nil
}

func (w *geodataLogWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer += string(data)
	for {
		index := strings.IndexByte(w.buffer, '\n')
		if index < 0 {
			break
		}
		line := w.buffer[:index+1]
		w.buffer = w.buffer[index+1:]
		if err := w.line(line); err != nil && !w.sinkFailed {
			w.sinkFailed = true
			logger.Warning("write Xray error log: ", err)
		}
	}
	// Bound unterminated output, including crash dumps from custom cores.
	if len(w.buffer) > 64*1024 {
		_, err := w.writer.Write([]byte(w.buffer))
		w.buffer = ""
		if err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func (w *geodataLogWriter) line(line string) error {
	match := logLineRegex.FindStringSubmatch(strings.TrimSpace(line))
	if len(match) > 3 {
		w.writer.captureGeodataEvent(match)
		limit, ok := nativeLogSeverity[w.level]
		if !ok {
			limit = 1
		}
		severity, known := nativeLogSeverity[strings.ToLower(match[2])]
		if w.path == "none" || limit < 0 || known && severity > limit {
			return nil
		}
		if w.file != nil {
			_, err := io.WriteString(w.file, line)
			return err
		}
	}
	_, err := w.writer.Write([]byte(line))
	return err
}

func (w *geodataLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buffer != "" {
		_ = w.line(w.buffer)
		w.buffer = ""
	}
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

func (lw *LogWriter) captureGeodataEvent(match []string) {
	body := match[3]
	kind := ""
	switch {
	case strings.HasPrefix(body, "app/geodata: scheduled geodata reload failed"):
		kind = "failed"
	case strings.HasPrefix(body, "app/geodata: downloading geodata asset from "):
		kind = "download"
	case strings.HasPrefix(body, "common/geodata: reloading GeoIP data for "):
		kind = "ip-start"
	case strings.HasPrefix(body, "common/geodata: reloaded GeoIP data for "):
		kind = "ip-ok"
	case strings.HasPrefix(body, "common/geodata: reloading GeoSite data for "):
		kind = "site-start"
	case strings.HasPrefix(body, "common/geodata: reloaded GeoSite data for "):
		kind = "site-ok"
	}
	if kind == "" {
		return
	}
	target := ""
	if kind == "download" {
		if index := strings.LastIndex(body, " to "); index >= 0 {
			target = body[index+4:]
		}
	}
	at, err := time.ParseInLocation("2006/01/02 15:04:05.999999999", match[1], time.Local)
	if err != nil {
		at = time.Now()
	}
	lw.mu.Lock()
	defer lw.mu.Unlock()
	lw.geodataSequence++
	lw.geodataEvents = append(lw.geodataEvents, GeodataEvent{Sequence: lw.geodataSequence, At: at, Kind: kind, Message: body, Target: target})
	if len(lw.geodataEvents) > 256 {
		lw.geodataEvents = lw.geodataEvents[len(lw.geodataEvents)-256:]
	}
}
