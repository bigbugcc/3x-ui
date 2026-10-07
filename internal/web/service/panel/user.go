package panel

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
	ldaputil "github.com/mhsanaei/3x-ui/v3/internal/util/ldap"
	"github.com/mhsanaei/3x-ui/v3/internal/util/totp"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// UserService provides business logic for user management and authentication.
// It handles user creation, login, password management, and 2FA operations.
type UserService struct {
	settingService service.SettingService
}

// GetFirstUser retrieves the first user from the database.
// This is typically used for initial setup or when there's only one admin user.
func (s *UserService) GetFirstUser() (*model.User, error) {
	db := database.GetDB()

	user := &model.User{}
	err := db.Model(model.User{}).
		First(user).
		Error
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *UserService) CheckUser(username string, password string, twoFactorCode string) (*model.User, error) {
	db := database.GetDB()

	user := &model.User{}

	err := db.Model(model.User{}).
		Where("username = ?", username).
		First(user).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.New("invalid credentials")
	} else if err != nil {
		logger.Warning("check user err:", err)
		return nil, err
	}

	if !crypto.CheckPasswordHash(user.Password, password) {
		ldapEnabled, _ := s.settingService.GetLdapEnable()
		if !ldapEnabled {
			return nil, errors.New("invalid credentials")
		}

		host, _ := s.settingService.GetLdapHost()
		port, _ := s.settingService.GetLdapPort()
		useTLS, _ := s.settingService.GetLdapUseTLS()
		skipVerify, _ := s.settingService.GetLdapInsecureSkipVerify()
		bindDN, _ := s.settingService.GetLdapBindDN()
		ldapPass, _ := s.settingService.GetLdapPassword()
		baseDN, _ := s.settingService.GetLdapBaseDN()
		userFilter, _ := s.settingService.GetLdapUserFilter()
		userAttr, _ := s.settingService.GetLdapUserAttr()

		cfg := ldaputil.Config{
			Host:               host,
			Port:               port,
			UseTLS:             useTLS,
			InsecureSkipVerify: skipVerify,
			BindDN:             bindDN,
			Password:           ldapPass,
			BaseDN:             baseDN,
			UserFilter:         userFilter,
			UserAttr:           userAttr,
		}
		ok, err := ldaputil.AuthenticateUser(cfg, username, password)
		if err != nil || !ok {
			return nil, errors.New("invalid credentials")
		}
	}

	twoFactorEnable, err := s.settingService.GetTwoFactorEnable()
	if err != nil {
		logger.Warning("check two factor err:", err)
		return nil, err
	}

	if twoFactorEnable {
		twoFactorToken, err := s.settingService.GetTwoFactorToken()
		if err != nil {
			logger.Warning("check two factor token err:", err)
			return nil, err
		}

		if !totp.VerifyWithSkew(twoFactorToken, twoFactorCode, time.Now()) {
			return nil, errors.New("invalid 2fa code")
		}
	}

	return user, nil
}

func (s *UserService) BumpLoginEpoch() error {
	service.AuthenticationStateMu.Lock()
	defer service.AuthenticationStateMu.Unlock()
	db := database.GetDB()
	return db.Transaction(service.InvalidateAuthentication)
}

func (s *UserService) UpdateUser(id int, username string, password string) error {
	_, err := s.UpdateUserAtEpoch(id, username, password, -1)
	return err
}

func (s *UserService) UpdateUserAtEpoch(id int, username, password string, expectedEpoch int64) (*model.User, error) {
	service.AuthenticationStateMu.Lock()
	defer service.AuthenticationStateMu.Unlock()
	db := database.GetDB()
	hashedPassword, err := crypto.HashPasswordAsBcrypt(password)
	if err != nil {
		return nil, err
	}

	twoFactorEnable, err := s.settingService.GetTwoFactorEnable()
	if err != nil {
		return nil, err
	}

	var updated model.User
	err = db.Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&model.User{}).Where("id = ?", id)
		if expectedEpoch >= 0 {
			query = query.Where("login_epoch = ?", expectedEpoch)
		}
		result := query.Updates(map[string]any{
			"username":    username,
			"password":    hashedPassword,
			"login_epoch": gorm.Expr("login_epoch + 1"),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return service.ErrAuthenticationChanged
		}
		if twoFactorEnable {
			if err := tx.Model(&model.Setting{}).Where("key = ?", "twoFactorEnable").Update("value", "false").Error; err != nil {
				return err
			}
			if err := tx.Model(&model.Setting{}).Where("key = ?", "twoFactorToken").Update("value", "").Error; err != nil {
				return err
			}
		}
		if err := tx.Where("user_id = ?", id).Delete(&model.PasskeyCredential{}).Error; err != nil {
			return err
		}
		return tx.First(&updated, id).Error
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *UserService) UpdateFirstUser(username string, password string) error {
	service.AuthenticationStateMu.Lock()
	defer service.AuthenticationStateMu.Unlock()
	if username == "" {
		return errors.New("username can not be empty")
	} else if password == "" {
		return errors.New("password can not be empty")
	}
	hashedPassword, er := crypto.HashPasswordAsBcrypt(password)

	if er != nil {
		return er
	}

	db := database.GetDB()
	user := &model.User{}
	err := db.Model(model.User{}).First(user).Error
	if database.IsNotFound(err) {
		user.Username = username
		user.Password = hashedPassword
		return db.Model(model.User{}).Create(user).Error
	} else if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.User{}).Where("id = ?", user.Id).Updates(map[string]any{
			"username": username, "password": hashedPassword,
			"login_epoch": gorm.Expr("login_epoch + 1"),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return service.ErrAuthenticationChanged
		}
		return tx.Where("user_id = ?", user.Id).Delete(&model.PasskeyCredential{}).Error
	})
}
