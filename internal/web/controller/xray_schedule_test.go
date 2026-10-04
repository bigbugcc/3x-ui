package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"

	"github.com/mhsanaei/3x-ui/v3/internal/web/locale"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

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
}

func TestXrayScheduleAPIRoutesRequireAdmin(t *testing.T) {
	engine := scheduleTestRoutes(t)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/panel/api/xray/schedule/restart"},
		{http.MethodPost, "/panel/api/xray/schedule/restart"},
		{http.MethodPost, "/panel/api/xray/schedule/restart/run"},
		{http.MethodGet, "/panel/api/xray/schedule/geodata"},
		{http.MethodPost, "/panel/api/xray/schedule/geodata"},
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
