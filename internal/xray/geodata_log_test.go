package xray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeodataLogCapturePreservesDefaultAccessLogging(t *testing.T) {
	for _, log := range []string{"", `,"log":null`, `,"log":{}`} {
		t.Run(log, func(t *testing.T) {
			input := []byte(`{"geodata":{"cron":"@every 1s"}` + log + `}`)
			data, writer, err := NewLogWriter().prepareGeodataLogging(input)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = writer.Close() })
			var cfg struct {
				Log struct {
					Access string `json:"access"`
				} `json:"log"`
			}
			if err := json.Unmarshal(data, &cfg); err != nil {
				t.Fatal(err)
			}
			want := "none"
			if log == `,"log":{}` {
				want = ""
			}
			if cfg.Log.Access != want {
				t.Fatalf("access log = %q, want %q", cfg.Log.Access, want)
			}
		})
	}
}

func TestGeodataLogCapturePreservesSettingsAndHandlesSplitOutput(t *testing.T) {
	for _, level := range []string{"warning", "error", "none", "debug"} {
		t.Run(level, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "error.log")
			input, _ := json.Marshal(map[string]any{"geodata": map[string]string{"cron": "@every 1s"}, "log": map[string]any{"loglevel": level, "error": path, "dnsLog": true}})
			lw := NewLogWriter()
			data, writer, err := lw.prepareGeodataLogging(input)
			if err != nil {
				t.Fatal(err)
			}
			var runtimeCfg struct {
				Log struct {
					Level  string `json:"loglevel"`
					Error  string `json:"error"`
					DNS    bool   `json:"dnsLog"`
					Access string `json:"access"`
				} `json:"log"`
			}
			if err := json.Unmarshal(data, &runtimeCfg); err != nil {
				t.Fatal(err)
			}
			if runtimeCfg.Log.Error != "" || !runtimeCfg.Log.DNS || level == "none" && runtimeCfg.Log.Access != "none" {
				t.Fatalf("runtime config: %s", data)
			}
			output := "2026/10/04 15:00:00.000001 [Info] common/geodata: reloading GeoIP data for 0 IP matcher(s)\n2026/10/04 15:00:00.000002 [Info] common/geodata: reloaded GeoIP data for 0 IP matcher(s)\n2026/10/04 15:00:00.000003 [Info] common/geodata: reloading GeoSite data for 0 domain matcher(s)\n2026/10/04 15:00:00.000004 [Info] common/geodata: reloaded GeoSite data for 0 domain matcher(s)\n2026/10/04 15:00:00.000005 [Error] app/geodata: scheduled geodata reload failed > test error\n"
			for _, b := range []byte(output) {
				if _, err := writer.Write([]byte{b}); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			events := lw.GeodataEvents(0)
			if len(events) != 5 || events[4].Kind != "failed" || events[3].At.Sub(events[0].At).Microseconds() != 3 {
				t.Fatalf("split events: %+v", events)
			}
			if len(lw.GeodataEvents(events[3].Sequence)) != 1 {
				t.Fatal("events replayed")
			}
			logged, readErr := os.ReadFile(path)
			if level == "none" {
				if !os.IsNotExist(readErr) {
					t.Fatal("disabled error file was created")
				}
				return
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !strings.Contains(string(logged), "test error") {
				t.Fatal("original error output lost")
			}
			if level != "debug" && strings.Contains(string(logged), "[Info]") {
				t.Fatal("internal observation changed ordinary log verbosity")
			}
		})
	}
}
