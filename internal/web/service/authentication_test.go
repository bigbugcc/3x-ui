package service

import (
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestPasskeyImportedAuthenticationIsAtomic(t *testing.T) {
	setupSettingTestDB(t)
	db := database.GetDB()
	imported := model.PasskeyConfig{Id: 1, Version: 10, Data: `{"enabled":true,"rpId":"source.example.com"}`}
	kept := model.PasskeyConfig{Id: 1, Version: 5, Data: `{"enabled":false,"rpId":"destination.example.com"}`}
	credential := model.PasskeyCredential{UserId: 1, RPID: "source.example.com", CredentialId: "source-id", Name: "source", CredentialData: "source-data"}
	mapping := model.WebAuthnUser{UserId: 1, RPID: "source.example.com", UserHandle: "source-handle"}
	proxy := model.Setting{Key: "trustedProxyCIDRs", Value: "192.0.2.1/32"}
	for _, row := range []any{&imported, &credential, &mapping, &proxy} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	var before model.User
	if err := db.First(&before).Error; err != nil {
		t.Fatal(err)
	}
	const callbackName = "test:reject_import_epoch"
	if err := db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "User" {
			tx.AddError(errors.New("forced epoch update failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Update().Remove(callbackName) })
	if err := finalizeImportedAuthentication(db, true, kept, ""); err == nil {
		t.Fatal("expected authentication finalization failure")
	}
	var current model.PasskeyConfig
	if err := db.First(&current, 1).Error; err != nil || current != imported {
		t.Fatalf("failed finalization changed configuration: %v %#v", err, current)
	}
	value, err := (&SettingService{}).GetTrustedProxyCIDRs()
	if err != nil || value != proxy.Value {
		t.Fatalf("failed finalization changed proxy trust: %v %q", err, value)
	}
	for _, row := range []any{&model.PasskeyCredential{}, &model.WebAuthnUser{}} {
		var count int64
		if err := db.Model(row).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("failed finalization changed credentials: %v count=%d", err, count)
		}
	}
	if err := db.Callback().Update().Remove(callbackName); err != nil {
		t.Fatal(err)
	}
	if err := finalizeImportedAuthentication(db, true, kept, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&current, 1).Error; err != nil || current.Data != kept.Data || current.Version == kept.Version || current.Version > 1<<53-1 {
		t.Fatalf("kept configuration not restored with a fresh version: %v %#v", err, current)
	}
	value, err = (&SettingService{}).GetTrustedProxyCIDRs()
	if err != nil || value != "" {
		t.Fatalf("import did not preserve explicit empty proxy trust: %v %q", err, value)
	}
	var after model.User
	if err := db.First(&after).Error; err != nil || after.LoginEpoch == before.LoginEpoch {
		t.Fatalf("import did not revoke sessions: %v", err)
	}
	for _, row := range []any{&model.PasskeyCredential{}, &model.WebAuthnUser{}} {
		var count int64
		if err := db.Model(row).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("import retained foreign credentials: %v count=%d", err, count)
		}
	}
}

func TestPasskeyDirectHTTPSIsEnforcedByService(t *testing.T) {
	setupSettingTestDB(t)
	cfg := validPasskeyConfig()
	cfg.HTTPSMode = "direct"
	if _, err := (&PasskeyConfigService{}).Save(cfg, 0); !errors.Is(err, ErrPasskeyDirectHTTPS) {
		t.Fatalf("direct HTTPS without a certificate accepted: %v", err)
	}
	view, err := (&PasskeyConfigService{}).Get()
	if err != nil || view.Version != 0 || view.Config.Enabled {
		t.Fatalf("rejected configuration was persisted: %v %#v", err, view)
	}
}
