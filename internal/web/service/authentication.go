package service

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"sync"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// AuthenticationStateMu serializes final authentication and security mutations on this
// single-instance panel, including the session save following verification.
var AuthenticationStateMu sync.RWMutex

var ErrAuthenticationChanged = errors.New("authentication state changed; retry with current credentials")

// Epochs revoke sessions and grants; the version rejects anonymous ceremonies.
// Callers serialize security changes in the same transaction with AuthenticationStateMu.
func InvalidateAuthentication(tx *gorm.DB) error {
	if err := tx.Model(&model.User{}).Where("1 = 1").Update("login_epoch", gorm.Expr("login_epoch + 1")).Error; err != nil {
		return err
	}
	return tx.Model(&model.PasskeyConfig{}).Where("id = 1").Update("version", gorm.Expr("version + 1")).Error
}

// CLI recovery must update both TOTP fields and invalidate prior authorization
// in one transaction, including when the panel is running in another process.
func (s *SettingService) ResetTwoFactorAuthentication() error {
	AuthenticationStateMu.Lock()
	defer AuthenticationStateMu.Unlock()
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		for key, value := range map[string]string{"twoFactorEnable": "false", "twoFactorToken": ""} {
			result := tx.Model(&model.Setting{}).Where("key = ?", key).Update("value", value)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				if err := tx.Create(&model.Setting{Key: key, Value: value}).Error; err != nil {
					return err
				}
			}
		}
		return InvalidateAuthentication(tx)
	})
}

// Fresh random epochs cannot be rolled back by restoring an old backup. Every
// restore invalidates cookies even when the signing secret or old epoch repeats.
func RefreshRestoredAuthentication(db *gorm.DB) error {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return err
	}
	epoch := int64(binary.BigEndian.Uint64(buf[:])&((1<<62)-1)) + 1
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.User{}).Where("1 = 1").Update("login_epoch", epoch).Error; err != nil {
			return err
		}
		// JSON clients use IEEE-754 numbers; keep the optimistic version exactly
		// representable, independently of the larger server-only cookie epoch.
		return tx.Model(&model.PasskeyConfig{}).Where("id = 1").Update("version", (epoch>>11)+1).Error
	})
}

func finalizeImportedAuthentication(db *gorm.DB, keepHostSettings bool, keptPasskey model.PasskeyConfig, keptProxy string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if keepHostSettings {
			// This also covers SQLite-to-PostgreSQL imports, which replace settings.
			if err := tx.Where("key = ?", "trustedProxyCIDRs").Delete(&model.Setting{}).Error; err != nil {
				return err
			}
			if err := tx.Create(&model.Setting{Key: "trustedProxyCIDRs", Value: keptProxy}).Error; err != nil {
				return err
			}
			for _, row := range []any{&model.PasskeyConfig{}, &model.PasskeyCredential{}, &model.WebAuthnUser{}} {
				if err := tx.Where("1 = 1").Delete(row).Error; err != nil {
					return err
				}
			}
			if keptPasskey.Id != 0 {
				if err := tx.Create(&keptPasskey).Error; err != nil {
					return err
				}
			}
		}
		return RefreshRestoredAuthentication(tx)
	})
}
