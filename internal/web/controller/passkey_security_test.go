package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"
)

// Simulate a separate CLI process resetting credentials after password checking.
func resetEpochDuringReauthentication(t *testing.T) {
	t.Helper()
	db := database.GetDB()
	var reset atomic.Bool
	const name = "test:concurrent_passkey_recovery"
	if err := db.Callback().Query().After("gorm:query").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Setting" && reset.CompareAndSwap(false, true) {
			if err := db.Model(&model.User{}).Where("id = 1").Update("login_epoch", gorm.Expr("login_epoch + 1")).Error; err != nil {
				t.Error(err)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Query().Remove(name) })
}

func TestPasskeyGrantCannotAdoptRecoveryEpoch(t *testing.T) {
	b := newPasskeyTestBrowser(t)
	b.enable()
	resetEpochDuringReauthentication(t)
	grant := b.authorization("register", 0)
	b.passwordLogin()
	w, _ := b.request("POST", "/panel/api/setting/passkeys/register/begin", map[string]string{"name": "Attacker", "authorizationId": grant})
	if w.Code == http.StatusOK {
		t.Fatal("old password verification adopted the new recovery epoch")
	}
}

func TestPasskeyConfigRejectsConcurrentRecovery(t *testing.T) {
	b := newPasskeyTestBrowser(t)
	b.enable()
	before, err := (&service.PasskeyConfigService{}).Get()
	if err != nil {
		t.Fatal(err)
	}
	resetEpochDuringReauthentication(t)
	next := before.Config
	next.Enabled = false
	w, _ := b.request("POST", "/panel/api/setting/passkeys/config", map[string]any{"config": next, "expectedVersion": before.Version, "currentPassword": passkeyTestPassword})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("stale authentication saved deployment policy: %d %s", w.Code, w.Body.String())
	}
	after, err := (&service.PasskeyConfigService{}).Get()
	if err != nil || after.Version != before.Version || !after.Config.Enabled {
		t.Fatalf("stale authentication changed configuration: %v %#v", err, after)
	}
}

func TestGeneralSettingsRechecksRevokedSessionAfterBodyRead(t *testing.T) {
	b := newPasskeyTestBrowser(t)
	settings, err := (&service.SettingService{}).GetAllSetting()
	if err != nil {
		t.Fatal(err)
	}
	settings.WebBasePath = "/attacker/"
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	req := httptest.NewRequest(http.MethodPost, passkeyTestOrigin+"/secret/panel/api/setting/update", reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(session.CSRFHeaderName, b.csrf)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	for _, cookie := range b.cookies {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); b.engine.ServeHTTP(w, req) }()
	if _, err := writer.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	defer func() { writer.Close(); <-done }()
	if err := (&service.SettingService{}).ResetTwoFactorAuthentication(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(body[1:]); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	<-done
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session continued its setting mutation: %d %s", w.Code, w.Body.String())
	}
}

func TestPasswordSlowBodyDoesNotBlockSecurityMutations(t *testing.T) {
	b := newPasskeyTestBrowser(t)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	req := httptest.NewRequest(http.MethodPost, passkeyTestOrigin+"/secret/login", reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(session.CSRFHeaderName, b.csrf)
	for _, cookie := range b.cookies {
		req.AddCookie(cookie)
	}
	done := make(chan struct{})
	go func() { defer close(done); b.engine.ServeHTTP(httptest.NewRecorder(), req) }()
	if _, err := writer.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	defer func() { writer.Close(); <-done }()
	mutation := make(chan error, 1)
	go func() { mutation <- (&service.SettingService{}).ResetTwoFactorAuthentication() }()
	select {
	case err := <-mutation:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("anonymous slow password request blocked account recovery")
	}
}

func TestPasskeyIdentitySubstitutionAndSignedFlags(t *testing.T) {
	for _, test := range []struct {
		name   string
		flags  byte
		handle string
	}{
		{"missing user presence", 4, ""},
		{"changed backup eligibility", 13, ""},
		{"backup without eligibility", 21, ""},
		{"wrong user handle", 5, "d3Jvbmc"},
		{"missing user handle", 5, "-"},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := newPasskeyTestBrowser(t)
			b.enable()
			authenticator := b.register()
			b.request("POST", "/logout", nil)
			b.refreshCSRF()
			options := b.begin("/passkey/login/begin", map[string]any{})
			credential := authenticator.assertion(t, options, passkeyTestOrigin, test.flags, 1)
			response := credential["response"].(map[string]any)
			if test.handle == "-" {
				delete(response, "userHandle")
			} else if test.handle != "" {
				response["userHandle"] = test.handle
			}
			w, _ := b.request("POST", "/passkey/login/finish", map[string]any{"ceremonyId": options.CeremonyID, "credential": credential})
			if w.Code == http.StatusOK {
				t.Fatal("invalid identity or authenticator flags accepted")
			}
			w, _ = b.request("GET", "/panel/api/setting/passkeys", nil)
			if w.Code == http.StatusOK {
				t.Fatal("rejected assertion issued an authenticated session")
			}
		})
	}
}

func TestPasskeyRegistrationRejectsInvalidSignedFlags(t *testing.T) {
	for _, flags := range []byte{0x41, 0x44, 0x55} {
		b := newPasskeyTestBrowser(t)
		b.enable()
		grant := b.authorization("register", 0)
		options := b.begin("/panel/api/setting/passkeys/register/begin", map[string]string{"name": "Attacker", "authorizationId": grant})
		credential, _ := passkeyCreation(t, options, flags)
		w, _ := b.request("POST", "/panel/api/setting/passkeys/register/finish", map[string]any{"ceremonyId": options.CeremonyID, "credential": credential})
		if w.Code == http.StatusOK {
			t.Fatalf("invalid registration flags accepted: %x", flags)
		}
		var count int64
		if err := database.GetDB().Model(&model.PasskeyCredential{}).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("invalid registration persisted credential: %v, %d", err, count)
		}
	}
}

func TestPasskeyChallengeCannotMoveBetweenBrowsers(t *testing.T) {
	b := newPasskeyTestBrowser(t)
	b.enable()
	authenticator := b.register()
	b.request("POST", "/logout", nil)
	b.refreshCSRF()
	options := b.begin("/passkey/login/begin", map[string]any{})
	credential := authenticator.assertion(t, options, passkeyTestOrigin, 5, 1)
	other := &passkeyTestBrowser{t: t, engine: b.engine}
	other.refreshCSRF()
	w, _ := other.request("POST", "/passkey/login/finish", map[string]any{"ceremonyId": options.CeremonyID, "credential": credential})
	if w.Code == http.StatusOK {
		t.Fatal("another browser adopted the ceremony")
	}
	w, _ = b.request("POST", "/passkey/login/finish", map[string]any{"ceremonyId": options.CeremonyID, "credential": credential})
	if w.Code != http.StatusOK {
		t.Fatalf("wrong browser consumed the legitimate challenge: %d %s", w.Code, w.Body.String())
	}
}

func TestPasskeyRecoverySuspensionRejectsCookieAndLogin(t *testing.T) {
	b := newPasskeyTestBrowser(t)
	b.enable()
	database.AuthenticationSuspended.Store(true)
	t.Cleanup(func() { database.AuthenticationSuspended.Store(false) })
	w, _ := b.request("GET", "/panel/api/setting/passkeys", nil)
	if w.Code == http.StatusOK {
		t.Fatal("old cookie authenticated against a partially restored database")
	}
	w, _ = b.request("POST", "/passkey/login/begin", map[string]any{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("anonymous ceremony started during recovery: %d", w.Code)
	}
	w, result := b.request("POST", "/login", map[string]string{"username": "admin", "password": passkeyTestPassword})
	if result.Success || w.Code == http.StatusOK {
		t.Fatal("password login issued a session during recovery")
	}
}
