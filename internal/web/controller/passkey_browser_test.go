package controller

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/session"
)

// Opt-in because this needs a built frontend and Playwright Chromium.
// The invocation is documented in docs/passkey.md.
func TestPasskeyBrowser(t *testing.T) {
	if os.Getenv("XUI_PASSKEY_BROWSER_TEST") != "1" {
		t.Skip("opt-in Playwright integration check")
	}
	b := newPasskeyTestBrowser(t)
	oldDist := distFS
	SetDistFS(os.DirFS(".."))
	defer SetDistFS(oldDist)
	b.engine.Static("/secret/assets", "../dist/assets")
	pageHandler := func(c *gin.Context) {
		if session.GetBrowserLoginUser(c) == nil {
			c.Redirect(302, "/secret/")
			return
		}
		serveDistPage(c, "index.html")
	}
	b.engine.GET("/secret/panel/", pageHandler)
	b.engine.GET("/secret/panel/settings", pageHandler)
	server := httptest.NewTLSServer(b.engine)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	url := strings.Replace(server.URL, "127.0.0.1", "localhost", 1) + "/secret/"
	cmd := exec.CommandContext(ctx, "node", "scripts/passkey-browser-check.mjs", url, filepath.Join(root, ".cache", "passkey-browser"))
	cmd.Dir = filepath.Join(root, "frontend")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser check: %v\n%s", err, output)
	}
	t.Log(string(output))
}
