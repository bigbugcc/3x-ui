package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/panel"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"
)

var defaultPasskeyStore = panel.NewPasskeyStore()

var passkeyBeginLimiter = newLoginLimiter(20, time.Minute, time.Minute)
var passkeyFailureLimiter = newLoginLimiter(5, 5*time.Minute, 15*time.Minute)

type PasskeyController struct {
	config   service.PasskeyConfigService
	passkeys panel.PasskeyService
}

func passkeyGuard(browser bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if browser && session.GetBrowserLoginUser(c) == nil {
			passkeyError(c, http.StatusUnauthorized, "passkey.errors.session")
			c.Abort()
			return
		}
		if c.Request.Method == http.MethodGet {
			service.AuthenticationStateMu.RLock()
			defer service.AuthenticationStateMu.RUnlock()
		} else {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
			if !session.ValidateCSRFToken(c) {
				passkeyError(c, http.StatusForbidden, "passkey.errors.csrf")
				c.Abort()
				return
			}
			// Finish bounded network reads before serializing authentication.
			body, err := io.ReadAll(c.Request.Body)
			if err != nil {
				passkeyBodyError(c, err)
				c.Abort()
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
			service.AuthenticationStateMu.Lock()
			defer service.AuthenticationStateMu.Unlock()
		}
		// Recheck the epoch after waiting; a security mutation may revoke it.
		if database.AuthenticationSuspended.Load() {
			passkeyError(c, http.StatusServiceUnavailable, "passkey.errors.unavailable")
			c.Abort()
			return
		}
		if browser && session.GetBrowserLoginUser(c) == nil {
			passkeyError(c, http.StatusUnauthorized, "passkey.errors.session")
			c.Abort()
			return
		}
		c.Next()
	}
}

func registerPasskeyPublic(g *gin.RouterGroup) {
	a := &PasskeyController{}
	g = g.Group("/passkey", passkeyGuard(false))
	g.GET("/status", a.status)
	g.POST("/login/begin", a.loginBegin)
	g.POST("/login/finish", a.loginFinish)
}

func registerPasskeyManagement(g *gin.RouterGroup) {
	a := &PasskeyController{}
	g = g.Group("/passkeys", passkeyGuard(true))
	g.GET("", a.list)
	g.GET("/config", a.getConfig)
	g.POST("/config/validate", a.validateConfig)
	g.POST("/config", a.saveConfig)
	g.POST("/reauth", a.reauth)
	g.POST("/register/begin", a.registerBegin)
	g.POST("/register/finish", a.registerFinish)
	g.POST("/rename/:id", a.rename)
	g.POST("/delete/:id", a.delete)
}

func passkeyError(c *gin.Context, status int, key string) {
	c.JSON(status, gin.H{"success": false, "msg": I18nWeb(c, key)})
}

func passkeyBind(c *gin.Context, value any) bool {
	if err := c.ShouldBindJSON(value); err != nil {
		passkeyBodyError(c, err)
		return false
	}
	return true
}

func passkeyBodyError(c *gin.Context, err error) {
	status := http.StatusBadRequest
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		status = http.StatusRequestEntityTooLarge
	}
	passkeyError(c, status, "passkey.errors.input")
}

func passkeyRequestOrigin(c *gin.Context, cfg service.PasskeySettings) string {
	if !SecureBrowserRequest(c) || (cfg.HTTPSMode == "direct" && c.Request.TLS == nil) {
		return ""
	}
	u, err := url.Parse("https://" + c.Request.Host)
	if err != nil {
		return ""
	}
	origin := "https://" + strings.ToLower(u.Hostname())
	if u.Port() != "" && u.Port() != "443" {
		origin += ":" + u.Port()
	}
	return origin
}

func (a *PasskeyController) active(c *gin.Context) (*service.PasskeyConfigView, bool) {
	cfg, err := a.config.Get()
	if err != nil || !cfg.Config.Enabled {
		passkeyError(c, http.StatusBadRequest, "passkey.errors.unavailable")
		return nil, false
	}
	// Shared proxy settings also participate in validity of every ceremony.
	normalized, issues := service.NormalizePasskeySettings(cfg.Config)
	cfg.Config = normalized
	if len(issues) != 0 || !slices.Contains(cfg.Config.Origins, passkeyRequestOrigin(c, cfg.Config)) {
		passkeyError(c, http.StatusBadRequest, "passkey.errors.origin")
		return nil, false
	}
	return cfg, true
}

func (a *PasskeyController) status(c *gin.Context) {
	cfg, err := a.config.Get()
	if err != nil {
		passkeyError(c, http.StatusServiceUnavailable, "passkey.errors.unavailable")
		return
	}
	_, issues := service.NormalizePasskeySettings(cfg.Config)
	jsonObj(c, gin.H{"enabled": cfg.Config.Enabled, "available": cfg.Config.Enabled && len(issues) == 0 && slices.Contains(cfg.Config.Origins, passkeyRequestOrigin(c, cfg.Config))}, nil)
}

func (a *PasskeyController) getConfig(c *gin.Context) {
	cfg, err := a.config.Get()
	if err != nil {
		passkeyError(c, http.StatusServiceUnavailable, "passkey.errors.unavailable")
		return
	}
	twoFactor, err := (&service.SettingService{}).GetTwoFactorEnable()
	if err != nil {
		passkeyError(c, http.StatusServiceUnavailable, "passkey.errors.unavailable")
		return
	}
	_, issues := service.NormalizePasskeySettings(cfg.Config)
	jsonObj(c, gin.H{"config": cfg.Config, "version": cfg.Version, "twoFactorEnabled": twoFactor, "validationErrors": issues, "currentOriginAllowed": slices.Contains(cfg.Config.Origins, passkeyRequestOrigin(c, cfg.Config))}, nil)
}

func (a *PasskeyController) validateConfig(c *gin.Context) {
	var form struct {
		Config service.PasskeySettings `json:"config"`
	}
	if !passkeyBind(c, &form) {
		return
	}
	cfg, issues := service.NormalizePasskeySettings(form.Config)
	jsonObj(c, gin.H{"config": cfg, "errors": issues, "currentOriginAllowed": slices.Contains(cfg.Origins, passkeyRequestOrigin(c, cfg))}, nil)
}

func (a *PasskeyController) saveConfig(c *gin.Context) {
	var form struct {
		Config          service.PasskeySettings `json:"config"`
		ExpectedVersion int64                   `json:"expectedVersion"`
		CurrentPassword string                  `json:"currentPassword"`
		TwoFactorCode   string                  `json:"twoFactorCode"`
	}
	if !passkeyBind(c, &form) {
		return
	}
	user := a.verifyPassword(c, form.CurrentPassword, form.TwoFactorCode)
	if user == nil {
		return
	}
	userID := user.Id
	cfg, issues := service.NormalizePasskeySettings(form.Config)
	if len(issues) != 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "msg": I18nWeb(c, "passkey.errors.input"), "obj": gin.H{"errors": issues}})
		return
	}
	result, err := a.config.SaveAtEpoch(cfg, form.ExpectedVersion, user)
	if errors.Is(err, service.ErrAuthenticationChanged) {
		passkeyError(c, http.StatusUnauthorized, "passkey.errors.session")
		return
	}
	if errors.Is(err, service.ErrPasskeyDirectHTTPS) {
		passkeyError(c, http.StatusBadRequest, "passkey.errors.directHTTPS")
		return
	}
	if errors.Is(err, service.ErrPasskeyConfigConflict) {
		passkeyError(c, http.StatusConflict, "passkey.errors.conflict")
		return
	}
	if err != nil {
		logger.Warning("passkey configuration save failed:", err)
		passkeyError(c, http.StatusServiceUnavailable, "passkey.errors.save")
		return
	}
	defaultPasskeyStore.Clear()
	logger.Infof("passkey configuration changed: user_id=%d, IP=%q", userID, getRemoteIp(c))
	jsonObj(c, gin.H{"config": result.Config, "version": result.Version, "restartRequired": false, "reauthRequired": true}, nil)
}

func sessionUserID(c *gin.Context) int {
	u := session.GetBrowserLoginUser(c)
	if u == nil {
		return 0
	}
	return u.Id
}

func (a *PasskeyController) verifyPassword(c *gin.Context, password, code string) *model.User {
	u := session.GetBrowserLoginUser(c)
	ip := getRemoteIp(c)
	if u == nil {
		passkeyError(c, http.StatusUnauthorized, "passkey.errors.session")
		return nil
	}
	if _, allowed := defaultLoginLimiter.allow(ip, u.Username); !allowed {
		passkeyError(c, http.StatusTooManyRequests, "passkey.errors.rateLimit")
		return nil
	}
	if err := a.passkeys.Reauthenticate(u, password, code); err != nil {
		defaultLoginLimiter.registerFailure(ip, u.Username)
		logger.Warningf("passkey management authentication failed: user_id=%d, IP=%q", u.Id, ip)
		passkeyError(c, http.StatusBadRequest, "passkey.errors.verify")
		return nil
	}
	return u
}

func (a *PasskeyController) list(c *gin.Context) {
	rows, err := a.passkeys.List(sessionUserID(c))
	if err != nil {
		passkeyError(c, http.StatusServiceUnavailable, "passkey.errors.unavailable")
		return
	}
	jsonObj(c, rows, nil)
}

func (a *PasskeyController) reauth(c *gin.Context) {
	var form struct {
		CurrentPassword, TwoFactorCode, Purpose string
		TargetID                                int `json:"targetId"`
	}
	if !passkeyBind(c, &form) {
		return
	}
	if form.Purpose != "register" && form.Purpose != "delete" {
		passkeyError(c, http.StatusBadRequest, "passkey.errors.input")
		return
	}
	u := a.verifyPassword(c, form.CurrentPassword, form.TwoFactorCode)
	if u == nil {
		return
	}
	cfg, err := a.config.Get()
	if err != nil {
		passkeyError(c, http.StatusServiceUnavailable, "passkey.errors.unavailable")
		return
	}
	id, err := defaultPasskeyStore.Put(panel.PasskeyCeremony{Binding: session.BrowserBinding(c), Purpose: "authorize:" + form.Purpose, UserID: u.Id, TargetID: form.TargetID, Epoch: u.LoginEpoch, Version: cfg.Version}, 5*time.Minute)
	if err != nil {
		passkeyError(c, http.StatusTooManyRequests, "passkey.errors.rateLimit")
		return
	}
	jsonObj(c, gin.H{"authorizationId": id}, nil)
}

func (a *PasskeyController) authorize(c *gin.Context, id, purpose string, target int, cfg *service.PasskeyConfigView) bool {
	row, err := defaultPasskeyStore.Take(id, session.BrowserBinding(c), "authorize:"+purpose)
	u := session.GetBrowserLoginUser(c)
	if err != nil || u == nil || row.UserID != u.Id || row.Epoch != u.LoginEpoch || row.Version != cfg.Version || row.TargetID != target {
		passkeyError(c, http.StatusBadRequest, "passkey.errors.expired")
		return false
	}
	return true
}

func (a *PasskeyController) registerBegin(c *gin.Context) {
	var form struct{ Name, AuthorizationID string }
	if !passkeyBind(c, &form) {
		return
	}
	cfg, ok := a.active(c)
	if !ok {
		return
	}
	name, err := panel.PasskeyName(form.Name)
	if err != nil {
		passkeyError(c, http.StatusBadRequest, "passkey.errors.name")
		return
	}
	if !a.authorize(c, form.AuthorizationID, "register", 0, cfg) {
		return
	}
	u := session.GetBrowserLoginUser(c)
	options, data, err := a.passkeys.BeginRegistration(u, cfg.Config)
	if errors.Is(err, panel.ErrPasskeyLimit) {
		passkeyError(c, http.StatusConflict, "passkey.errors.limit")
		return
	}
	if err != nil {
		passkeyError(c, http.StatusServiceUnavailable, "passkey.errors.unavailable")
		return
	}
	id, err := defaultPasskeyStore.Put(panel.PasskeyCeremony{Binding: session.BrowserBinding(c), Purpose: "register", UserID: u.Id, Epoch: u.LoginEpoch, Version: cfg.Version, Origin: passkeyRequestOrigin(c, cfg.Config), Name: name, Data: data}, 120*time.Second)
	if err != nil {
		passkeyError(c, http.StatusTooManyRequests, "passkey.errors.rateLimit")
		return
	}
	jsonObj(c, gin.H{"ceremonyId": id, "publicKey": options.Response}, nil)
}

type passkeyFinishForm struct {
	CeremonyID string          `json:"ceremonyId"`
	Credential json.RawMessage `json:"credential"`
}

func (a *PasskeyController) registerFinish(c *gin.Context) {
	var form passkeyFinishForm
	if !passkeyBind(c, &form) {
		return
	}
	row, err := defaultPasskeyStore.Take(form.CeremonyID, session.BrowserBinding(c), "register")
	if err != nil {
		passkeyError(c, http.StatusGone, "passkey.errors.expired")
		return
	}
	cfg, ok := a.active(c)
	if !ok {
		return
	}
	u := session.GetBrowserLoginUser(c)
	if row.UserID != u.Id || row.Epoch != u.LoginEpoch || row.Version != cfg.Version || row.Origin != passkeyRequestOrigin(c, cfg.Config) {
		passkeyError(c, http.StatusBadRequest, "passkey.errors.expired")
		return
	}
	if err := a.passkeys.FinishRegistration(u, cfg, row, form.Credential); err != nil {
		passkeyError(c, http.StatusBadRequest, "passkey.errors.verify")
		return
	}
	logger.Infof("passkey registered: user_id=%d, IP=%q", u.Id, getRemoteIp(c))
	jsonMsg(c, I18nWeb(c, "passkey.added"), nil)
}

func (a *PasskeyController) loginBegin(c *gin.Context) {
	ip := getRemoteIp(c)
	if _, ok := passkeyBeginLimiter.allow(ip, "passkey-begin"); !ok {
		passkeyError(c, http.StatusTooManyRequests, "passkey.errors.rateLimit")
		return
	}
	passkeyBeginLimiter.registerFailure(ip, "passkey-begin")
	if _, ok := passkeyFailureLimiter.allow(ip, "passkey-finish"); !ok {
		passkeyError(c, http.StatusTooManyRequests, "passkey.errors.rateLimit")
		return
	}
	cfg, ok := a.active(c)
	if !ok {
		return
	}
	options, data, err := a.passkeys.BeginLogin(cfg.Config)
	if err != nil {
		passkeyError(c, http.StatusServiceUnavailable, "passkey.errors.unavailable")
		return
	}
	id, err := defaultPasskeyStore.Put(panel.PasskeyCeremony{Binding: session.BrowserBinding(c), Purpose: "login", Version: cfg.Version, Origin: passkeyRequestOrigin(c, cfg.Config), Data: data}, 120*time.Second)
	if err != nil {
		passkeyError(c, http.StatusTooManyRequests, "passkey.errors.rateLimit")
		return
	}
	jsonObj(c, gin.H{"ceremonyId": id, "publicKey": options.Response}, nil)
}

func (a *PasskeyController) loginFinish(c *gin.Context) {
	ip := getRemoteIp(c)
	if _, ok := passkeyFailureLimiter.allow(ip, "passkey-finish"); !ok {
		passkeyError(c, http.StatusTooManyRequests, "passkey.errors.rateLimit")
		return
	}
	var form passkeyFinishForm
	if !passkeyBind(c, &form) {
		a.failLogin(c, nil)
		return
	}
	row, err := defaultPasskeyStore.Take(form.CeremonyID, session.BrowserBinding(c), "login")
	if err != nil {
		a.failLogin(c, nil)
		return
	}
	cfg, ok := a.active(c)
	if !ok {
		return
	}
	if row.Version != cfg.Version || row.Origin != passkeyRequestOrigin(c, cfg.Config) {
		a.failLogin(c, nil)
		return
	}
	result, err := a.passkeys.FinishLogin(cfg, row, form.Credential, func(user *model.User) bool {
		_, allowed := defaultLoginLimiter.allow(ip, user.Username)
		return allowed
	})
	if err != nil {
		var user *model.User
		if result != nil {
			user = result.User
		}
		a.failLogin(c, user)
		return
	}
	if result.Credential.Authenticator.CloneWarning {
		logger.Warningf("passkey counter risk signal: user_id=%d, credential_id=%d", result.User.Id, result.RecordID)
	}
	defaultPasskeyStore.ClearBinding(session.BrowserBinding(c))
	(&IndexController{}).completeLogin(c, result.User, "passkey")
}

func (a *PasskeyController) failLogin(c *gin.Context, user *model.User) {
	ip := getRemoteIp(c)
	passkeyFailureLimiter.registerFailure(ip, "passkey-finish")
	if user != nil {
		defaultLoginLimiter.registerFailure(ip, user.Username)
	}
	logger.Warningf("passkey login failed: IP=%q", ip)
	if !c.Writer.Written() {
		passkeyError(c, http.StatusBadRequest, "passkey.errors.verify")
	}
}

func (a *PasskeyController) rename(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		passkeyError(c, http.StatusBadRequest, "passkey.errors.input")
		return
	}
	var form struct{ Name string }
	if !passkeyBind(c, &form) {
		return
	}
	name, err := panel.PasskeyName(form.Name)
	if err != nil {
		passkeyError(c, http.StatusBadRequest, "passkey.errors.name")
		return
	}
	if err := a.passkeys.Rename(sessionUserID(c), id, name); err != nil {
		passkeyError(c, http.StatusBadRequest, "passkey.errors.verify")
		return
	}
	jsonMsg(c, I18nWeb(c, "passkey.saved"), nil)
}

func (a *PasskeyController) delete(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		passkeyError(c, http.StatusBadRequest, "passkey.errors.input")
		return
	}
	var form struct{ AuthorizationID string }
	if !passkeyBind(c, &form) {
		return
	}
	cfg, err := a.config.Get()
	if err != nil {
		passkeyError(c, http.StatusServiceUnavailable, "passkey.errors.unavailable")
		return
	}
	if !a.authorize(c, form.AuthorizationID, "delete", id, cfg) {
		return
	}
	u := session.GetBrowserLoginUser(c)
	if err := a.passkeys.Delete(u, id); err != nil {
		passkeyError(c, http.StatusBadRequest, "passkey.errors.verify")
		return
	}
	defaultPasskeyStore.ClearBinding(session.BrowserBinding(c))
	logger.Infof("passkey deleted: user_id=%d, record_id=%d, IP=%q", u.Id, id, getRemoteIp(c))
	jsonObj(c, gin.H{"reauthRequired": true}, nil)
}
