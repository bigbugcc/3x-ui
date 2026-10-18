package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"

	"github.com/mhsanaei/3x-ui/v3/internal/web/locale"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func TestGeodataScheduleCanceledRequestDoesNotSave(t *testing.T) {
	engine := scheduleTestRoutes(t)
	settings := &service.XraySettingService{}
	before, err := settings.GetXrayConfigTemplate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/test/xray/schedule/geodata", strings.NewReader(`{"enabled":false,"cron":"0 5 * * *","timezone":"UTC","assets":[]}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	var response geodataEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Success || !strings.Contains(response.Msg, context.Canceled.Error()) {
		t.Fatalf("canceled request was accepted: %+v", response)
	}
	after, err := settings.GetXrayConfigTemplate()
	if err != nil || after != before {
		t.Fatalf("canceled request changed the template: %v", err)
	}
	view, err := settings.GetGeodataSchedule()
	if err != nil || view.Config.Cron == "0 5 * * *" {
		t.Fatalf("canceled request changed the draft: %+v, %v", view, err)
	}
}

type geodataDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *geodataDeadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestGeodataScheduleTimeoutScopeAndBudget(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		wantBudget   bool
	}{
		{http.MethodPost, "/custom/panel/api/xray/schedule/geodata", true},
		{http.MethodGet, "/custom/panel/api/xray/schedule/geodata", false},
		{http.MethodPost, "/custom/panel/api/xray/schedule/restart", false},
		{http.MethodPost, "/panel/api/xray/schedule/geodata", false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			engine := gin.New()
			engine.Use(GeodataScheduleTimeout("/custom/"))
			var deadline time.Time
			engine.Handle(tc.method, tc.path, func(c *gin.Context) {
				deadline, _ = c.Request.Context().Deadline()
				c.Status(http.StatusOK)
			})
			w := &geodataDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			start := time.Now()
			engine.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if tc.wantBudget {
				if remaining := deadline.Sub(start); remaining < 4*time.Minute || remaining > 4*time.Minute+time.Second {
					t.Fatalf("unexpected operation deadline: %v", remaining)
				}
				if remaining := w.deadline.Sub(start); remaining < 5*time.Minute || remaining > 5*time.Minute+time.Second {
					t.Fatalf("unexpected write deadline: %v", remaining)
				}
			} else if !deadline.IsZero() || !w.deadline.IsZero() {
				t.Fatal("Geo timeout policy changed an unrelated request")
			}
		})
	}
}

func TestGeodataScheduleTimeoutWithGzipAndRealHTTP(t *testing.T) {
	for _, basePath := range []string{"/", "/custom/"} {
		t.Run(basePath, func(t *testing.T) {
			engine := gin.New()
			engine.Use(GeodataScheduleTimeout(basePath))
			engine.Use(gzip.Gzip(gzip.DefaultCompression))
			path := basePath + "panel/api/xray/schedule/geodata"
			engine.POST(path, func(c *gin.Context) {
				// A slow first deployment must survive the server's normal write window.
				time.Sleep(250 * time.Millisecond)
				c.JSON(http.StatusOK, gin.H{"success": true})
			})
			srv := httptest.NewUnstartedServer(engine)
			srv.Config.WriteTimeout = 50 * time.Millisecond
			srv.Start()
			defer srv.Close()
			client := srv.Client()
			client.Timeout = 2 * time.Second
			resp, err := client.Post(srv.URL+path, "application/json", strings.NewReader(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"success":true`) {
				t.Fatalf("slow Geo response failed: %d %s, %v", resp.StatusCode, body, err)
			}
		})
	}
}

func TestGeodataScheduleTimeoutPreservesEarlierDeadline(t *testing.T) {
	engine := gin.New()
	engine.Use(GeodataScheduleTimeout("/"))
	engine.POST("/panel/api/xray/schedule/geodata", func(c *gin.Context) {
		<-c.Request.Context().Done()
		jsonObj(c, nil, c.Request.Context().Err())
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	w := &geodataDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/panel/api/xray/schedule/geodata", nil).WithContext(ctx))
	if !strings.Contains(w.Body.String(), context.DeadlineExceeded.Error()) {
		t.Fatalf("request deadline was replaced: %s", w.Body.String())
	}
}

func scheduleTestRoutes(t *testing.T) *gin.Engine {
	t.Helper()
	engine, auth := newAPIAuthTestEngine(t)
	c := cron.New(cron.WithSeconds())
	c.Start()
	t.Cleanup(func() { <-c.Stop().Done() })
	scheduler := service.NewXrayScheduler(context.Background(), c)
	if err := scheduler.Restore(); err != nil {
		t.Fatal(err)
	}
	// Production registers these routes under the authenticated API group.
	group := engine.Group("/test")
	group.Use(func(c *gin.Context) {
		c.Set("I18n", func(_ locale.I18nType, key string, _ ...string) string { return key })
	})
	NewXrayScheduleController(group, scheduler)
	protected := engine.Group("/panel/api")
	protected.Use(auth.checkAPIAuth, auth.enforceTokenScope)
	NewXrayScheduleController(protected, scheduler)
	return engine
}

func scheduleRequest(t *testing.T, engine *gin.Engine, method, path, body string) geodataEnvelope {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	var response geodataEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("response = %s: %v", w.Body.String(), err)
	}
	return response
}

func TestXrayScheduleAPIValidationAndSavedStatus(t *testing.T) {
	engine := scheduleTestRoutes(t)
	response := scheduleRequest(t, engine, http.MethodPost, "/test/xray/schedule/restart", `{"enabled":true,"cron":"99 4 * * *","timezone":"UTC"}`)
	if response.Success {
		t.Fatal("API accepted invalid cron")
	}
	response = scheduleRequest(t, engine, http.MethodPost, "/test/xray/schedule/restart", `{"enabled":true,"cron":"0 4 * * *","timezone":"Asia/Shanghai"}`)
	var view service.XrayRestartScheduleView
	if err := json.Unmarshal(response.Obj, &view); err != nil {
		t.Fatal(err)
	}
	if !response.Success || !view.Config.Enabled || view.NextRun == 0 {
		t.Fatalf("save = %+v", response)
	}
	response = scheduleRequest(t, engine, http.MethodPost, "/test/xray/schedule/geodata", `{"enabled":false,"cron":"0 4 * * *","timezone":"UTC","outbound":"","assets":[]}`)
	var geo service.GeodataScheduleView
	if err := json.Unmarshal(response.Obj, &geo); err != nil {
		t.Fatal(err)
	}
	if !response.Success || geo.Config.Enabled || geo.Applied {
		t.Fatalf("stopped core save = %+v", response)
	}
	response = scheduleRequest(t, engine, http.MethodPost, "/test/xray/schedule/geodata", `{"enabled":true,"cron":"0 4 * * *","timezone":"UTC","assets":[{"file":"missing.dat","url":"https://example.com/missing.dat"}]}`)
	if response.Success {
		t.Fatal("API enabled a missing asset")
	}
	if !strings.Contains(response.Msg, "pages.xray.schedule.geoFileRequired") || !strings.Contains(response.Msg, "missing.dat") {
		t.Fatalf("missing asset response lost localized explanation or technical detail: %+v", response)
	}
}

func TestXrayScheduleAPIRoutesRequireAdmin(t *testing.T) {
	engine := scheduleTestRoutes(t)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/panel/api/xray/schedule/restart"},
		{http.MethodPost, "/panel/api/xray/schedule/restart"},
		{http.MethodPost, "/panel/api/xray/schedule/restart/run"},
		{http.MethodGet, "/panel/api/xray/schedule/geodata"},
		{http.MethodPost, "/panel/api/xray/schedule/geodata"},
		{http.MethodGet, "/panel/api/xray/schedule/logs"},
		{http.MethodPost, "/panel/api/xray/schedule/logs/settings"},
		{http.MethodPost, "/panel/api/xray/schedule/logs/clear"},
	} {
		req := httptest.NewRequest(route.method, route.path, nil)
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s = %d", route.path, w.Code)
		}
		req = httptest.NewRequest(route.method, route.path, nil)
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
		w = httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("node-sync %s = %d", route.path, w.Code)
		}
	}
}
