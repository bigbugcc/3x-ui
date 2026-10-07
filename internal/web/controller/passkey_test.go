package controller

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/middleware"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/panel"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"
)

const passkeyTestOrigin = "https://panel.example.test"
const passkeyTestPassword = "passkey-test-password"

func TestPasskeySlowBodyDoesNotBlockAuthentication(t *testing.T) {
	b := newPasskeyTestBrowser(t)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	req := httptest.NewRequest(http.MethodPost, passkeyTestOrigin+"/secret/passkey/login/begin", reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(session.CSRFHeaderName, b.csrf)
	for _, cookie := range b.cookies {
		req.AddCookie(cookie)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.engine.ServeHTTP(httptest.NewRecorder(), req)
	}()
	// The first byte proves the request is blocked waiting for the remaining body.
	if _, err := writer.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	defer func() { writer.Close(); <-done }()
	status := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		b.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, passkeyTestOrigin+"/secret/passkey/status", nil))
		status <- w.Code
	}()
	select {
	case code := <-status:
		if code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("slow request blocked authentication state reads")
	}
}

func TestPasskeyRechecksSessionAfterBodyRead(t *testing.T) {
	b := newPasskeyTestBrowser(t)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	req := httptest.NewRequest(http.MethodPost, passkeyTestOrigin+"/secret/panel/api/setting/passkeys/reauth", reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(session.CSRFHeaderName, b.csrf)
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
	if _, err := writer.Write([]byte(`"purpose":"register","currentPassword":"` + passkeyTestPassword + `"}`)); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	<-done
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestPasskeyBodySizeLimit(t *testing.T) {
	b := newPasskeyTestBrowser(t)
	w, _ := b.request(http.MethodPost, "/passkey/login/begin", map[string]string{"padding": strings.Repeat("x", 64<<10)})
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d", w.Code)
	}
}

func TestPasskeyPasswordRecoveryPreservesConcurrentEpoch(t *testing.T) {
	newPasskeyTestBrowser(t)
	db := database.GetDB()
	var before model.User
	if err := db.First(&before).Error; err != nil {
		t.Fatal(err)
	}
	var changed atomic.Bool
	const callbackName = "test:concurrent_password_epoch"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "User" && changed.CompareAndSwap(false, true) {
			tx.AddError(db.Model(&model.User{}).Where("id = ?", before.Id).Update("login_epoch", gorm.Expr("login_epoch + 1")).Error)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Query().Remove(callbackName)
	if err := (&panel.UserService{}).UpdateFirstUser("admin", "new-recovery-password"); err != nil {
		t.Fatal(err)
	}
	var after model.User
	if err := db.First(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !changed.Load() || after.LoginEpoch != before.LoginEpoch+2 {
		t.Fatalf("concurrent epoch lost: before=%d after=%d", before.LoginEpoch, after.LoginEpoch)
	}
}

func TestPasskeyPasswordUpdateReturnsItsOwnEpoch(t *testing.T) {
	newPasskeyTestBrowser(t)
	users := &panel.UserService{}
	before, err := users.GetFirstUser()
	if err != nil {
		t.Fatal(err)
	}
	updated, err := users.UpdateUserAtEpoch(before.Id, "admin", "my-password", before.LoginEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := users.UpdateFirstUser("admin", "later-recovery-password"); err != nil {
		t.Fatal(err)
	}
	after, err := users.GetFirstUser()
	if err != nil {
		t.Fatal(err)
	}
	if updated.LoginEpoch != before.LoginEpoch+1 || after.LoginEpoch != updated.LoginEpoch+1 {
		t.Fatal("password update adopted another password change's session epoch")
	}
	if _, err := users.UpdateUserAtEpoch(before.Id, "admin", "stale-password", updated.LoginEpoch); !errors.Is(err, service.ErrAuthenticationChanged) {
		t.Fatalf("stale password update accepted: %v", err)
	}
}

func TestPasskeyDebugLogsExcludeCredentialMaterial(t *testing.T) {
	b := newPasskeyTestBrowser(t)
	b.enable()
	db := database.GetDB()
	var output bytes.Buffer
	previousLogger := db.Logger
	db.Logger = gormlogger.New(log.New(&output, "", 0), gormlogger.Config{LogLevel: gormlogger.Info})
	t.Cleanup(func() { db.Logger = previousLogger })
	b.register()
	var credential model.PasskeyCredential
	var mapping model.WebAuthnUser
	if err := db.First(&credential).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	if credential.CredentialData == "" || mapping.UserHandle == "" {
		t.Fatal("expected persisted credential and handle")
	}
	if strings.Contains(output.String(), credential.CredentialData) || strings.Contains(output.String(), mapping.UserHandle) {
		t.Fatal("debug SQL logged credential material or stable handle")
	}
}

type passkeyTestBrowser struct {
	t       *testing.T
	engine  *gin.Engine
	cookies []*http.Cookie
	csrf    string
}

func newPasskeyTestBrowser(t *testing.T) *passkeyTestBrowser {
	t.Helper()
	newHostTestDB(t)
	if err := (&panel.UserService{}).UpdateFirstUser("admin", passkeyTestPassword); err != nil {
		t.Fatal(err)
	}
	defaultPasskeyStore = panel.NewPasskeyStore()
	defaultLoginLimiter = newLoginLimiter(5, 5*time.Minute, 15*time.Minute)
	passkeyBeginLimiter = newLoginLimiter(20, time.Minute, time.Minute)
	passkeyFailureLimiter = newLoginLimiter(5, 5*time.Minute, 15*time.Minute)
	engine := gin.New()
	store := cookie.NewStore([]byte("01234567890123456789012345678901"))
	engine.Use(sessions.Sessions("3x-ui", store))
	engine.Use(func(c *gin.Context) {
		c.Set("base_path", "/secret/")
		c.Set("session_secure", SecureBrowserRequest(c))
		sessions.Default(c).Options(sessions.Options{Path: "/secret/", Secure: SecureBrowserRequest(c), HttpOnly: true, SameSite: http.SameSiteLaxMode})
	})
	g := engine.Group("/secret/")
	NewIndexController(g)
	a := &APIController{}
	api := g.Group("/panel/api", a.checkAPIAuth, a.enforceTokenScope, middleware.CSRFMiddleware())
	NewSettingController(api)
	b := &passkeyTestBrowser{t: t, engine: engine}
	b.refreshCSRF()
	b.passwordLogin()
	return b
}

func (b *passkeyTestBrowser) request(method, path string, body any) (*httptest.ResponseRecorder, hostEnvelope) {
	b.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		b.t.Fatal(err)
	}
	req := httptest.NewRequest(method, passkeyTestOrigin+"/secret"+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if b.csrf != "" {
		req.Header.Set(session.CSRFHeaderName, b.csrf)
	}
	for _, value := range b.cookies {
		req.AddCookie(value)
	}
	w := httptest.NewRecorder()
	b.engine.ServeHTTP(w, req)
	for _, value := range w.Result().Cookies() {
		if value.MaxAge < 0 {
			if value.Path == "/secret/" {
				b.cookies = nil
			}
			continue
		}
		b.cookies = []*http.Cookie{value}
		if !value.Secure || !value.HttpOnly || value.Path != "/secret/" {
			b.t.Fatalf("unsafe cookie: %#v", value)
		}
	}
	var result hostEnvelope
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			b.t.Fatalf("invalid JSON: %s", w.Body.String())
		}
	}
	return w, result
}

func (b *passkeyTestBrowser) refreshCSRF() {
	b.t.Helper()
	w, result := b.request("GET", "/csrf-token", nil)
	if w.Code != 200 || !result.Success {
		b.t.Fatalf("csrf: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(result.Obj, &b.csrf); err != nil {
		b.t.Fatal(err)
	}
}

func (b *passkeyTestBrowser) passwordLogin() {
	b.t.Helper()
	w, result := b.request("POST", "/login", map[string]string{"username": "admin", "password": passkeyTestPassword})
	if w.Code != 200 || !result.Success {
		b.t.Fatalf("password login: %d %s", w.Code, w.Body.String())
	}
	b.refreshCSRF()
}

func (b *passkeyTestBrowser) enable() {
	b.t.Helper()
	body := map[string]any{"config": map[string]any{"enabled": true, "rpId": "panel.example.test", "origins": []string{passkeyTestOrigin}, "httpsMode": "proxy", "trustedProxyCIDRs": "127.0.0.1/32,::1/128"}, "expectedVersion": 0, "currentPassword": passkeyTestPassword}
	w, result := b.request("POST", "/panel/api/setting/passkeys/config", body)
	if w.Code != 200 || !result.Success {
		b.t.Fatalf("enable: %d %s", w.Code, w.Body.String())
	}
	w, _ = b.request("GET", "/panel/api/setting/passkeys/config", nil)
	if w.Code != 401 {
		b.t.Fatal("configuration change did not invalidate old browser session")
	}
	b.passwordLogin()
}

type passkeyTestOptions struct {
	CeremonyID string `json:"ceremonyId"`
	PublicKey  struct {
		Challenge string `json:"challenge"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
		UserVerification       string                                         `json:"userVerification"`
		AuthenticatorSelection struct{ UserVerification, ResidentKey string } `json:"authenticatorSelection"`
	} `json:"publicKey"`
}

func (b *passkeyTestBrowser) begin(path string, body any) passkeyTestOptions {
	b.t.Helper()
	w, result := b.request("POST", path, body)
	if w.Code != 200 || !result.Success {
		b.t.Fatalf("begin: %d %s", w.Code, w.Body.String())
	}
	var options passkeyTestOptions
	if err := json.Unmarshal(result.Obj, &options); err != nil {
		b.t.Fatal(err)
	}
	return options
}

func (b *passkeyTestBrowser) authorization(purpose string, targetID int) string {
	b.t.Helper()
	w, result := b.request("POST", "/panel/api/setting/passkeys/reauth", map[string]any{"purpose": purpose, "targetId": targetID, "currentPassword": passkeyTestPassword})
	if w.Code != 200 || !result.Success {
		b.t.Fatalf("reauth: %d %s", w.Code, w.Body.String())
	}
	var authorization struct {
		ID string `json:"authorizationId"`
	}
	if err := json.Unmarshal(result.Obj, &authorization); err != nil {
		b.t.Fatal(err)
	}
	return authorization.ID
}

type passkeyTestAuthenticator struct {
	key    *ecdsa.PrivateKey
	id     []byte
	handle string
}

func testBase64(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

func (b *passkeyTestBrowser) register() *passkeyTestAuthenticator {
	return b.registerFlags(0x45)
}

func (b *passkeyTestBrowser) registerFlags(flags byte) *passkeyTestAuthenticator {
	b.t.Helper()
	authorization := b.authorization("register", 0)
	options := b.begin("/panel/api/setting/passkeys/register/begin", map[string]string{"name": "Test device", "authorizationId": authorization})
	if options.PublicKey.AuthenticatorSelection.UserVerification != "required" || options.PublicKey.AuthenticatorSelection.ResidentKey != "required" {
		b.t.Fatal("registration downgraded verification or discoverability")
	}
	w, _ := b.request("POST", "/panel/api/setting/passkeys/register/begin", map[string]string{"name": "Replay", "authorizationId": authorization})
	if w.Code == 200 {
		b.t.Fatal("authorization replay accepted")
	}
	credential, authenticator := passkeyCreation(b.t, options, flags)
	w, result := b.request("POST", "/panel/api/setting/passkeys/register/finish", map[string]any{"ceremonyId": options.CeremonyID, "credential": credential})
	if w.Code != 200 || !result.Success {
		b.t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	return authenticator
}

func passkeyCreation(t *testing.T, options passkeyTestOptions, flags byte) (map[string]any, *passkeyTestAuthenticator) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	cose, err := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: key.X.FillBytes(make([]byte, 32)), -3: key.Y.FillBytes(make([]byte, 32))})
	if err != nil {
		t.Fatal(err)
	}
	rpHash := sha256.Sum256([]byte("panel.example.test"))
	authData := append(append([]byte{}, rpHash[:]...), flags)
	authData = append(authData, make([]byte, 4+16)...)
	authData = append(authData, byte(len(id)>>8), byte(len(id)))
	authData = append(authData, id...)
	authData = append(authData, cose...)
	attestation, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "authData": authData, "attStmt": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	clientData, _ := json.Marshal(map[string]any{"type": "webauthn.create", "challenge": options.PublicKey.Challenge, "origin": passkeyTestOrigin, "crossOrigin": false})
	credential := map[string]any{"id": testBase64(id), "rawId": testBase64(id), "type": "public-key", "clientExtensionResults": map[string]any{"credProps": map[string]bool{"rk": true}}, "response": map[string]any{"clientDataJSON": testBase64(clientData), "attestationObject": testBase64(attestation), "transports": []string{"internal"}}}
	return credential, &passkeyTestAuthenticator{key: key, id: id, handle: options.PublicKey.User.ID}
}

func (a *passkeyTestAuthenticator) assertion(t *testing.T, options passkeyTestOptions, origin string, flags byte, count uint32) map[string]any {
	t.Helper()
	clientData, _ := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": options.PublicKey.Challenge, "origin": origin, "crossOrigin": false})
	rpHash := sha256.Sum256([]byte("panel.example.test"))
	authData := append(append([]byte{}, rpHash[:]...), flags)
	authData = binary.BigEndian.AppendUint32(authData, count)
	clientHash := sha256.Sum256(clientData)
	message := append(append([]byte{}, authData...), clientHash[:]...)
	digest := sha256.Sum256(message)
	signature, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"id": testBase64(a.id), "rawId": testBase64(a.id), "type": "public-key", "clientExtensionResults": map[string]any{}, "response": map[string]any{"clientDataJSON": testBase64(clientData), "authenticatorData": testBase64(authData), "signature": testBase64(signature), "userHandle": a.handle}}
}

func TestPasskeyRegistrationLoginReplayAndRevocation(t *testing.T) {
	b := newPasskeyTestBrowser(t)
	b.enable()
	authenticator := b.register()
	w, list := b.request("GET", "/panel/api/setting/passkeys", nil)
	if w.Code != 200 || strings.Contains(string(list.Obj), "credentialData") || strings.Contains(string(list.Obj), "publicKey") || strings.Contains(string(list.Obj), "userHandle") {
		t.Fatal("credential list exposed protocol material")
	}
	var rows []model.PasskeyCredential
	if err := json.Unmarshal(list.Obj, &rows); err != nil || len(rows) != 1 {
		t.Fatalf("list: %s", list.Obj)
	}
	b.request("POST", "/logout", nil)
	b.refreshCSRF()
	options := b.begin("/passkey/login/begin", map[string]any{})
	if options.PublicKey.UserVerification != "required" {
		t.Fatal("login does not require UV")
	}
	form := map[string]any{"ceremonyId": options.CeremonyID, "credential": authenticator.assertion(t, options, passkeyTestOrigin, 0x05, 1)}
	replay := *b
	replay.cookies = append([]*http.Cookie{}, b.cookies...)
	w, result := b.request("POST", "/passkey/login/finish", form)
	if w.Code != 200 || !result.Success {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var stored model.PasskeyCredential
	if err := database.GetDB().First(&stored, rows[0].Id).Error; err != nil {
		t.Fatal(err)
	}
	decoded, err := panel.DecodePasskeyCredential(stored.CredentialData)
	if err != nil || decoded.Authenticator.SignCount != 1 || decoded.Flags.ProtocolValue() != 0x05 || stored.LastUsedAt == 0 {
		t.Fatalf("credential state not persisted: %v %#v", err, decoded)
	}
	w, _ = replay.request("POST", "/passkey/login/finish", form)
	if w.Code == 200 {
		t.Fatal("signed anonymous cookie replayed a consumed challenge")
	}
	b.refreshCSRF()
	authorization := b.authorization("delete", rows[0].Id)
	w, _ = b.request("POST", "/panel/api/setting/passkeys/delete/"+strconv.Itoa(rows[0].Id), map[string]string{"authorizationId": authorization})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w, _ = b.request("GET", "/panel/api/setting/passkeys", nil)
	if w.Code != 401 {
		t.Fatal("revocation did not invalidate session")
	}
	b.passwordLogin()
	var count int64
	database.GetDB().Model(&model.PasskeyCredential{}).Count(&count)
	if count != 0 {
		t.Fatal("credential was not revoked")
	}
}

func TestPasskeyRejectsOriginSignatureAndMissingUV(t *testing.T) {
	b := newPasskeyTestBrowser(t)
	b.enable()
	view, err := (&service.PasskeyConfigService{}).Get()
	if err != nil {
		t.Fatal(err)
	}
	view.Config.Origins = append(view.Config.Origins, passkeyTestOrigin+":8443")
	service.AuthenticationStateMu.Lock()
	_, err = (&service.PasskeyConfigService{}).Save(view.Config, view.Version)
	service.AuthenticationStateMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	b.passwordLogin()
	authenticator := b.register()
	b.request("POST", "/logout", nil)
	b.refreshCSRF()
	for _, test := range []struct {
		name, origin string
		flags        byte
		corrupt      bool
	}{{"origin", "https://evil.example.test", 5, false}, {"other allowed origin", passkeyTestOrigin + ":8443", 5, false}, {"UV", passkeyTestOrigin, 1, false}, {"signature", passkeyTestOrigin, 5, true}} {
		t.Run(test.name, func(t *testing.T) {
			options := b.begin("/passkey/login/begin", map[string]any{})
			credential := authenticator.assertion(t, options, test.origin, test.flags, 1)
			if test.corrupt {
				credential["response"].(map[string]any)["signature"] = testBase64([]byte("bad-signature"))
			}
			w, _ := b.request("POST", "/passkey/login/finish", map[string]any{"ceremonyId": options.CeremonyID, "credential": credential})
			if w.Code == 200 {
				t.Fatal("invalid credential accepted")
			}
			w, _ = b.request("GET", "/panel/api/setting/passkeys", nil)
			if w.Code == 200 {
				t.Fatal("failed authentication created session")
			}
		})
	}
}

func TestPasskeyManagementRequiresBrowserCSRFAndReauthentication(t *testing.T) {
	b := newPasskeyTestBrowser(t)
	oldCSRF := b.csrf
	b.csrf = ""
	w, _ := b.request("POST", "/panel/api/setting/passkeys/reauth", map[string]any{"purpose": "register", "currentPassword": passkeyTestPassword})
	if w.Code != 403 {
		t.Fatal("management without CSRF accepted")
	}
	b.csrf = oldCSRF
	w, _ = b.request("POST", "/panel/api/setting/passkeys/reauth", map[string]any{"purpose": "register", "currentPassword": "wrong"})
	if w.Code == 200 {
		t.Fatal("wrong password issued grant")
	}
	settings := &service.SettingService{}
	if err := settings.SetTwoFactorToken("JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatal(err)
	}
	if err := settings.SetTwoFactorEnable(true); err != nil {
		t.Fatal(err)
	}
	w, _ = b.request("POST", "/panel/api/setting/passkeys/reauth", map[string]any{"purpose": "register", "currentPassword": passkeyTestPassword})
	if w.Code == 200 {
		t.Fatal("missing TOTP issued grant")
	}
	if err := settings.SetTwoFactorEnable(false); err != nil {
		t.Fatal(err)
	}
	token, err := (&panel.ApiTokenService{}).Create("passkey-test", model.ApiScopeAdmin, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"bearer", "mtls"} {
		req := httptest.NewRequest("GET", passkeyTestOrigin+"/secret/panel/api/setting/passkeys/config", nil)
		if mode == "bearer" {
			req.Header.Set("Authorization", "Bearer "+token.Token)
		} else {
			req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
		}
		w := httptest.NewRecorder()
		b.engine.ServeHTTP(w, req)
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("%s managed browser credentials: %d", mode, w.Code)
		}
	}
	// A bearer-authenticated request must not bypass the independent CSRF check
	// even when it also supplies a valid browser cookie.
	req := httptest.NewRequest("POST", passkeyTestOrigin+"/secret/panel/api/setting/passkeys/reauth", strings.NewReader(`{"purpose":"register","currentPassword":"`+passkeyTestPassword+`"}`))
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Content-Type", "application/json")
	for _, c := range b.cookies {
		req.AddCookie(c)
	}
	w = httptest.NewRecorder()
	b.engine.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("bearer bypassed browser CSRF")
	}
}

func TestPasskeyDeletionTargetAndPasswordReset(t *testing.T) {
	b := newPasskeyTestBrowser(t)
	b.enable()
	b.register()
	var row model.PasskeyCredential
	database.GetDB().First(&row)
	grant := b.authorization("delete", row.Id+1)
	w, _ := b.request("POST", "/panel/api/setting/passkeys/delete/"+strconv.Itoa(row.Id), map[string]string{"authorizationId": grant})
	if w.Code == 200 {
		t.Fatal("grant authorized a different deletion target")
	}
	w, _ = b.request("POST", "/panel/api/setting/update", map[string]any{"passkeyEnabled": false})
	if w.Code != 400 {
		t.Fatal("general settings accepted Passkey fields")
	}
	if err := (&panel.UserService{}).UpdateFirstUser("admin", "reset-password"); err != nil {
		t.Fatal(err)
	}
	w, _ = b.request("GET", "/panel/api/setting/passkeys", nil)
	if w.Code != 401 {
		t.Fatal("password reset retained session")
	}
	var count int64
	database.GetDB().Model(&model.PasskeyCredential{}).Count(&count)
	if count != 0 {
		t.Fatal("password reset retained Passkeys")
	}
}

func TestPasskeySignatureCounterPolicies(t *testing.T) {
	for _, test := range []struct {
		name          string
		flags         byte
		initial, next uint32
		success       bool
	}{
		{"zero counter", 0x45, 0, 0, true},
		{"device counter rollback", 0x45, 2, 1, false},
		{"synced counter rollback", 0x5d, 2, 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := newPasskeyTestBrowser(t)
			b.enable()
			authenticator := b.registerFlags(test.flags)
			b.request("POST", "/logout", nil)
			b.refreshCSRF()
			for i, counter := range []uint32{test.initial, test.next} {
				options := b.begin("/passkey/login/begin", map[string]any{})
				w, _ := b.request("POST", "/passkey/login/finish", map[string]any{"ceremonyId": options.CeremonyID, "credential": authenticator.assertion(t, options, passkeyTestOrigin, test.flags&^0x40, counter)})
				wantSuccess := i == 0 || test.success
				if (w.Code == 200) != wantSuccess {
					t.Fatalf("counter %d status %d: %s", counter, w.Code, w.Body.String())
				}
				if i == 0 {
					b.request("POST", "/logout", nil)
					b.refreshCSRF()
				}
			}
		})
	}
}

func TestPasskeyHTTPSModeAndProxyTrust(t *testing.T) {
	newHostTestDB(t)
	// Only the existing shared trust list controls forwarded HTTPS.
	database.GetDB().Where("key = ?", "trustedProxyCIDRs").Delete(&model.Setting{})
	database.GetDB().Create(&model.Setting{Key: "trustedProxyCIDRs", Value: "127.0.0.1/32"})
	for _, test := range []struct {
		mode, remote string
		tls, allowed bool
	}{{"direct", "127.0.0.1:1234", false, false}, {"proxy", "127.0.0.1:1234", false, true}, {"proxy", "192.0.2.5:1234", false, false}, {"direct", "192.0.2.5:1234", true, true}} {
		req := httptest.NewRequest("GET", "http://panel.example.test/", nil)
		req.RemoteAddr = test.remote
		req.Header.Set("X-Forwarded-Proto", "https")
		if test.tls {
			req.TLS = &tls.ConnectionState{}
		}
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = req
		if (passkeyRequestOrigin(ctx, service.PasskeySettings{HTTPSMode: test.mode}) != "") != test.allowed {
			t.Fatalf("incorrect HTTPS policy: %#v", test)
		}
	}
}
