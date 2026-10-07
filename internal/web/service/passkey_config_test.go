package service

import (
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func validPasskeyConfig() PasskeySettings {
	return PasskeySettings{Enabled: true, RPID: "panel.example.com", Origins: []string{"https://panel.example.com:8443"}, HTTPSMode: "proxy", TrustedProxyCIDRs: "127.0.0.1/32,::1/128"}
}

func TestPasskeySecuritySettingInvalidationIsAtomic(t *testing.T) {
	setupSettingTestDB(t)
	s := &SettingService{}
	cfg := &PasskeyConfigService{}
	AuthenticationStateMu.Lock()
	_, err := cfg.Save(validPasskeyConfig(), 0)
	AuthenticationStateMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetAllSetting()
	if err != nil {
		t.Fatal(err)
	}
	previousPath := before.WebBasePath
	var user model.User
	database.GetDB().First(&user)
	epoch := user.LoginEpoch
	before.WebBasePath = "/new-security-path/"
	// A failure invalidating authentication must roll back the changed settings.
	callbackName := "test:reject_passkey_version_update"
	if err := database.GetDB().Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "PasskeyConfig" {
			tx.AddError(errors.New("forced version failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.GetDB().Callback().Update().Remove(callbackName) })
	err = s.UpdateAllSetting(before, SecretClears{})
	if err == nil {
		t.Fatal("forced invalidation failure was ignored")
	}
	after, err := s.GetAllSetting()
	if err != nil || after.WebBasePath != previousPath {
		t.Fatal("security settings partially committed")
	}
	database.GetDB().First(&user)
	if user.LoginEpoch != epoch {
		t.Fatal("rolled-back settings invalidated sessions")
	}
	database.GetDB().Callback().Update().Remove(callbackName)
	if err := s.UpdateAllSetting(before, SecretClears{}); err != nil {
		t.Fatal(err)
	}
	database.GetDB().First(&user)
	view, _ := cfg.Get()
	if user.LoginEpoch != epoch+1 || view.Version != 2 {
		t.Fatal("security setting change did not invalidate pending authentication")
	}
	epoch = user.LoginEpoch
	if err := s.ResetTwoFactorAuthentication(); err != nil {
		t.Fatal(err)
	}
	database.GetDB().First(&user)
	view, _ = cfg.Get()
	if user.LoginEpoch != epoch+1 || view.Version != 3 {
		t.Fatal("CLI TOTP recovery reused authorization")
	}
}

func TestPasskeyConfigNormalization(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*PasskeySettings)
		field  string
	}{
		{"IP RP", func(c *PasskeySettings) { c.RPID = "127.0.0.1" }, "rpId"},
		{"URL RP", func(c *PasskeySettings) { c.RPID = "https://panel.example.com" }, "rpId"},
		{"empty RP", func(c *PasskeySettings) { c.RPID = "" }, "rpId"},
		{"invalid DNS", func(c *PasskeySettings) { c.RPID = "bad..example.com" }, "rpId"},
		{"HTTP origin", func(c *PasskeySettings) { c.Origins = []string{"http://panel.example.com"} }, "origins"},
		{"origin path", func(c *PasskeySettings) { c.Origins = []string{"https://panel.example.com/secret/"} }, "origins"},
		{"origin root path", func(c *PasskeySettings) { c.Origins = []string{"https://panel.example.com/"} }, "origins"},
		{"different host", func(c *PasskeySettings) { c.Origins = []string{"https://sub.panel.example.com"} }, "origins"},
		{"credentials", func(c *PasskeySettings) { c.Origins = []string{"https://u:p@panel.example.com"} }, "origins"},
		{"query", func(c *PasskeySettings) { c.Origins = []string{"https://panel.example.com?"} }, "origins"},
		{"empty fragment", func(c *PasskeySettings) { c.Origins = []string{"https://panel.example.com#"} }, "origins"},
		{"empty port", func(c *PasskeySettings) { c.Origins = []string{"https://panel.example.com:"} }, "origins"},
		{"bad port", func(c *PasskeySettings) { c.Origins = []string{"https://panel.example.com:65536"} }, "origins"},
		{"trust all", func(c *PasskeySettings) { c.TrustedProxyCIDRs = "0.0.0.0/0" }, "trustedProxyCIDRs"},
		{"trust all v6", func(c *PasskeySettings) { c.TrustedProxyCIDRs = "::/0" }, "trustedProxyCIDRs"},
		{"missing proxy", func(c *PasskeySettings) { c.TrustedProxyCIDRs = "" }, "trustedProxyCIDRs"},
		{"mode", func(c *PasskeySettings) { c.HTTPSMode = "auto" }, "httpsMode"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := validPasskeyConfig()
			test.change(&cfg)
			_, fields := NormalizePasskeySettings(cfg)
			if fields[test.field] == "" {
				t.Fatalf("accepted invalid config: %#v", cfg)
			}
		})
	}
	cfg := validPasskeyConfig()
	cfg.RPID = " PANEL.EXAMPLE.COM "
	cfg.Origins = []string{"https://PANEL.EXAMPLE.COM:00443", "https://panel.example.com", "https://panel.example.com:08443"}
	cfg.TrustedProxyCIDRs = "127.0.0.2/24,127.0.0.0/24, ::1"
	got, fields := NormalizePasskeySettings(cfg)
	if len(fields) != 0 || got.RPID != "panel.example.com" || len(got.Origins) != 2 || got.Origins[0] != "https://panel.example.com" || got.Origins[1] != "https://panel.example.com:8443" || got.TrustedProxyCIDRs != "127.0.0.0/24,::1" {
		t.Fatalf("normalization: %#v, %v", got, fields)
	}
	cfg.RPID = "例子.中国"
	cfg.Origins = []string{"https://例子.中国"}
	got, fields = NormalizePasskeySettings(cfg)
	if len(fields) != 0 || got.RPID != "xn--fsqu00a.xn--fiqs8s" {
		t.Fatalf("IDNA: %#v %v", got, fields)
	}
}

func TestPasskeyConfigSaveConflictAndRestore(t *testing.T) {
	setupSettingTestDB(t)
	s := &PasskeyConfigService{}
	initial, err := s.Get()
	if err != nil || initial.Version != 0 || initial.Config.Enabled {
		t.Fatalf("unsafe default: %#v %v", initial, err)
	}
	var user model.User
	database.GetDB().First(&user)
	oldEpoch := user.LoginEpoch
	AuthenticationStateMu.Lock()
	saved, err := s.Save(validPasskeyConfig(), 0)
	AuthenticationStateMu.Unlock()
	if err != nil || saved.Version != 1 {
		t.Fatalf("save: %#v %v", saved, err)
	}
	database.GetDB().First(&user)
	if user.LoginEpoch != oldEpoch+1 {
		t.Fatal("save did not invalidate sessions")
	}
	conflict := validPasskeyConfig()
	conflict.Enabled = false
	conflict.TrustedProxyCIDRs = "10.0.0.1"
	AuthenticationStateMu.Lock()
	_, err = s.Save(conflict, 0)
	AuthenticationStateMu.Unlock()
	if !errors.Is(err, ErrPasskeyConfigConflict) {
		t.Fatalf("stale save: %v", err)
	}
	unchanged, err := s.Get()
	if err != nil || !unchanged.Config.Enabled || unchanged.Config.TrustedProxyCIDRs != saved.Config.TrustedProxyCIDRs || unchanged.Version != saved.Version {
		t.Fatal("conflicting save partially changed configuration")
	}
	database.GetDB().First(&user)
	if user.LoginEpoch != oldEpoch+1 {
		t.Fatal("conflict invalidated sessions")
	}
	if err := RefreshRestoredAuthentication(database.GetDB()); err != nil {
		t.Fatal(err)
	}
	first, _ := s.Get()
	database.GetDB().First(&user)
	restoredEpoch := user.LoginEpoch
	if restoredEpoch == oldEpoch+1 || first.Version <= 0 || first.Version >= 1<<53 {
		t.Fatal("restored session/version was not safely randomized")
	}
	if err := RefreshRestoredAuthentication(database.GetDB()); err != nil {
		t.Fatal(err)
	}
	second, _ := s.Get()
	database.GetDB().First(&user)
	if first.Version == second.Version || user.LoginEpoch == restoredEpoch {
		t.Fatal("restoring twice reused an authentication generation")
	}
	AuthenticationStateMu.Lock()
	_, err = s.Save(conflict, second.Version)
	AuthenticationStateMu.Unlock()
	if err != nil {
		t.Fatal("restored config cannot be edited:", err)
	}
}
