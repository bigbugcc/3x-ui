package panel

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

var ErrPasskeyRejected = errors.New("passkey verification rejected")
var ErrPasskeyLimit = errors.New("passkey limit reached")

const MaxPasskeys = 10

type PasskeyUser struct {
	User        *model.User
	Handle      []byte
	Credentials []webauthn.Credential
}

func (u *PasskeyUser) WebAuthnID() []byte                         { return u.Handle }
func (u *PasskeyUser) WebAuthnName() string                       { return u.User.Username }
func (u *PasskeyUser) WebAuthnDisplayName() string                { return u.User.Username }
func (u *PasskeyUser) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

type credentialRecord struct {
	Version    int                         `json:"version"`
	Flags      protocol.AuthenticatorFlags `json:"flags"`
	Credential webauthn.Credential         `json:"credential"`
}

func EncodePasskeyCredential(credential *webauthn.Credential) (string, error) {
	data, err := json.Marshal(credentialRecord{Version: 1, Credential: *credential, Flags: credential.Flags.ProtocolValue()})
	return string(data), err
}

func DecodePasskeyCredential(data string) (*webauthn.Credential, error) {
	var record credentialRecord
	if err := json.Unmarshal([]byte(data), &record); err != nil {
		return nil, err
	}
	if record.Version != 1 || len(record.Credential.ID) == 0 || len(record.Credential.PublicKey) == 0 {
		return nil, ErrPasskeyRejected
	}
	flags := record.Credential.Flags
	record.Credential.Flags = webauthn.NewCredentialFlags(record.Flags)
	record.Credential.Flags.UserVerified = flags.UserVerified
	return &record.Credential, nil
}

func NewPasskeyWebAuthn(cfg service.PasskeySettings) (*webauthn.WebAuthn, error) {
	return webauthn.New(&webauthn.Config{
		RPID: cfg.RPID, RPDisplayName: "3x-ui", RPOrigins: cfg.Origins,
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: 120 * time.Second},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: 120 * time.Second},
		},
	})
}

type PasskeyService struct{}

// Debug SQL interpolation would expose credential material and stable handles.
func passkeyDB() *gorm.DB {
	return database.GetDB().Session(&gorm.Session{Logger: gormlogger.Discard})
}

// Management intentionally matches the existing local password + TOTP policy.
func (s *PasskeyService) Reauthenticate(user *model.User, password, code string) error {
	if user == nil || !crypto.CheckPasswordHash(user.Password, password) {
		return ErrPasskeyRejected
	}
	return (&service.SettingService{}).VerifyTwoFactorCode(code)
}

func PasskeyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 64 {
		return "", ErrPasskeyRejected
	}
	return name, nil
}

func (s *PasskeyService) List(userID int) ([]model.PasskeyCredential, error) {
	rows := []model.PasskeyCredential{}
	err := passkeyDB().Select("id", "rp_id", "name", "created_at", "last_used_at").Where("user_id = ?", userID).Order("created_at DESC, id DESC").Find(&rows).Error
	return rows, err
}

// Caller serializes this operation with security mutations. The unique index
// also prevents a second process from assigning a second handle to the account.
func (s *PasskeyService) loadUser(db *gorm.DB, user *model.User, rpID string, create bool) (*PasskeyUser, error) {
	var mapping model.WebAuthnUser
	err := db.Where("user_id = ? AND rp_id = ?", user.Id, rpID).First(&mapping).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && create {
		handle := make([]byte, 32)
		if _, err := rand.Read(handle); err != nil {
			return nil, err
		}
		mapping = model.WebAuthnUser{UserId: user.Id, RPID: rpID, UserHandle: base64.RawURLEncoding.EncodeToString(handle)}
		err = db.Create(&mapping).Error
	}
	if err != nil {
		return nil, err
	}
	handle, err := base64.RawURLEncoding.DecodeString(mapping.UserHandle)
	if err != nil || len(handle) != 32 {
		return nil, ErrPasskeyRejected
	}
	var rows []model.PasskeyCredential
	if err := db.Where("user_id = ? AND rp_id = ?", user.Id, rpID).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := &PasskeyUser{User: user, Handle: handle, Credentials: []webauthn.Credential{}}
	for _, row := range rows {
		credential, err := DecodePasskeyCredential(row.CredentialData)
		if err != nil {
			return nil, err
		}
		if base64.RawURLEncoding.EncodeToString(credential.ID) != row.CredentialId {
			return nil, ErrPasskeyRejected
		}
		result.Credentials = append(result.Credentials, *credential)
	}
	return result, nil
}

func (s *PasskeyService) Discover(rawID, handle []byte, rpID string) (*PasskeyUser, *model.PasskeyCredential, error) {
	var row model.PasskeyCredential
	db := passkeyDB()
	if err := db.Where("rp_id = ? AND credential_id = ?", rpID, base64.RawURLEncoding.EncodeToString(rawID)).First(&row).Error; err != nil {
		return nil, nil, ErrPasskeyRejected
	}
	var mapping model.WebAuthnUser
	if err := db.Where("rp_id = ? AND user_id = ? AND user_handle = ?", rpID, row.UserId, base64.RawURLEncoding.EncodeToString(handle)).First(&mapping).Error; err != nil {
		return nil, nil, ErrPasskeyRejected
	}
	var user model.User
	if err := db.First(&user, row.UserId).Error; err != nil {
		return nil, nil, ErrPasskeyRejected
	}
	adapter, err := s.loadUser(db, &user, rpID, false)
	return adapter, &row, err
}

func validatePasskeyState(tx *gorm.DB, user *model.User, version int64) error {
	var current model.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, user.Id).Error; err != nil {
		return err
	}
	if current.LoginEpoch != user.LoginEpoch {
		return ErrPasskeyRejected
	}
	cfg, err := service.ReadPasskeyConfig(tx)
	if err != nil {
		return err
	}
	if !cfg.Config.Enabled || cfg.Version != version {
		return ErrPasskeyRejected
	}
	return nil
}

func (s *PasskeyService) Register(user *model.User, cfg *service.PasskeyConfigView, name string, credential *webauthn.Credential) error {
	data, err := EncodePasskeyCredential(credential)
	if err != nil {
		return err
	}
	return passkeyDB().Transaction(func(tx *gorm.DB) error {
		if err := validatePasskeyState(tx, user, cfg.Version); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.PasskeyCredential{}).Where("user_id = ?", user.Id).Count(&count).Error; err != nil {
			return err
		}
		if count >= MaxPasskeys {
			return ErrPasskeyLimit
		}
		return tx.Create(&model.PasskeyCredential{UserId: user.Id, RPID: cfg.Config.RPID, CredentialId: base64.RawURLEncoding.EncodeToString(credential.ID), Name: name, CredentialData: data, CreatedAt: time.Now().UnixMilli()}).Error
	})
}

func (s *PasskeyService) RecordLogin(user *model.User, version int64, original *model.PasskeyCredential, credential *webauthn.Credential) error {
	if credential.Authenticator.CloneWarning && !credential.Flags.BackupEligible {
		return ErrPasskeyRejected
	}
	data, err := EncodePasskeyCredential(credential)
	if err != nil {
		return err
	}
	return passkeyDB().Transaction(func(tx *gorm.DB) error {
		if err := validatePasskeyState(tx, user, version); err != nil {
			return err
		}
		result := tx.Model(&model.PasskeyCredential{}).Where("id = ? AND user_id = ? AND credential_data = ?", original.Id, user.Id, original.CredentialData).Updates(map[string]any{"credential_data": data, "last_used_at": time.Now().UnixMilli()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrPasskeyRejected
		}
		return nil
	})
}

func (s *PasskeyService) Rename(userID, id int, name string) error {
	result := passkeyDB().Model(&model.PasskeyCredential{}).Where("id = ? AND user_id = ?", id, userID).Update("name", name)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrPasskeyRejected
	}
	return nil
}

func (s *PasskeyService) Delete(user *model.User, id int) error {
	return passkeyDB().Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND user_id = ?", id, user.Id).Delete(&model.PasskeyCredential{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrPasskeyRejected
		}
		update := tx.Model(&model.User{}).Where("id = ? AND login_epoch = ?", user.Id, user.LoginEpoch).Update("login_epoch", gorm.Expr("login_epoch + 1"))
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrPasskeyRejected
		}
		return nil
	})
}

func (s *PasskeyService) Reset(userID int) error {
	service.AuthenticationStateMu.Lock()
	defer service.AuthenticationStateMu.Unlock()
	return passkeyDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&model.PasskeyCredential{}).Error; err != nil {
			return err
		}
		return tx.Model(&model.User{}).Where("id = ?", userID).Update("login_epoch", gorm.Expr("login_epoch + 1")).Error
	})
}
