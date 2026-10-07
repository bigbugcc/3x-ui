package service

import (
	"errors"
	"os"
	"testing"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestAuthenticationRestoreFailsClosedAndRecoversAfterRestart(t *testing.T) {
	setupSettingTestDB(t)
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	t.Cleanup(func() { database.AuthenticationSuspended.Store(false) })
	db := database.GetDB()
	var before model.User
	if err := db.First(&before).Error; err != nil {
		t.Fatal(err)
	}
	imported := model.PasskeyConfig{Id: 1, Version: 10, Data: `{"enabled":true,"rpId":"imported.example.com"}`}
	kept := model.PasskeyConfig{Id: 1, Version: 5, Data: `{"enabled":false,"rpId":"local.example.com"}`}
	for _, row := range []any{&imported, &model.PasskeyCredential{UserId: before.Id, RPID: "imported.example.com", CredentialId: "imported-id", CredentialData: "imported-data"}, &model.WebAuthnUser{UserId: before.Id, RPID: "imported.example.com", UserHandle: "imported-handle"}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := authenticationRestore{Version: 1, KeepHost: true, Passkey: kept, Proxy: ""}
	if err := r.prepare(); err != nil {
		t.Fatal(err)
	}
	if !database.AuthenticationSuspended.Load() {
		t.Fatal("database replacement did not suspend authentication")
	}
	const callback = "test:fail_restore_authentication"
	if err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "User" {
			tx.AddError(errors.New("forced restore failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Update().Remove(callback) })
	if err := r.finalize(); err == nil {
		t.Fatal("failed authentication transaction accepted")
	}
	if _, err := os.Stat(authenticationRestorePath()); err != nil || !database.AuthenticationSuspended.Load() {
		t.Fatalf("failure removed the durable recovery barrier: %v", err)
	}
	if err := db.Callback().Update().Remove(callback); err != nil {
		t.Fatal(err)
	}
	// A new process has no in-memory barrier; the journal must restore it.
	database.AuthenticationSuspended.Store(false)
	if err := RecoverRestoredAuthentication(); err != nil {
		t.Fatal(err)
	}
	var after model.User
	if err := db.First(&after).Error; err != nil || after.LoginEpoch == before.LoginEpoch {
		t.Fatalf("recovery did not revoke backup cookies: %v", err)
	}
	if database.AuthenticationSuspended.Load() {
		t.Fatal("successful recovery did not resume authentication")
	}
	var cfg model.PasskeyConfig
	if err := db.First(&cfg, 1).Error; err != nil || cfg.Data != kept.Data || cfg.Version == kept.Version {
		t.Fatalf("journal lost the local deployment policy: %v, %#v", err, cfg)
	}
	for _, row := range []any{&model.PasskeyCredential{}, &model.WebAuthnUser{}} {
		var count int64
		if err := db.Model(row).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("recovery transferred foreign credentials: %v, %d", err, count)
		}
	}
	if _, err := os.Stat(authenticationRestorePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful recovery retained the journal: %v", err)
	}
}

func TestAuthenticationRestoreRejectsInvalidJournal(t *testing.T) {
	setupSettingTestDB(t)
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	t.Cleanup(func() { database.AuthenticationSuspended.Store(false) })
	for _, data := range []string{`{`, `{"version":2}`, `{"version":1,"unknown":true}`, `{"version":1} {}`} {
		if err := os.WriteFile(authenticationRestorePath(), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		database.AuthenticationSuspended.Store(false)
		if err := RecoverRestoredAuthentication(); err == nil || !database.AuthenticationSuspended.Load() {
			t.Fatalf("invalid journal enabled authentication: %q, %v", data, err)
		}
	}
}
