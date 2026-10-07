package service

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"golang.org/x/net/idna"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

var ErrPasskeyConfigConflict = errors.New("passkey configuration changed; reload and retry")
var ErrPasskeyDirectHTTPS = errors.New("direct HTTPS requires a valid panel certificate and key")

type PasskeySettings struct {
	Enabled           bool     `json:"enabled"`
	RPID              string   `json:"rpId"`
	Origins           []string `json:"origins"`
	HTTPSMode         string   `json:"httpsMode"`
	TrustedProxyCIDRs string   `json:"trustedProxyCIDRs"`
}

type PasskeyConfigView struct {
	Config  PasskeySettings `json:"config"`
	Version int64           `json:"version"`
}

type PasskeyConfigService struct{}

func (s *PasskeyConfigService) Get() (*PasskeyConfigView, error) {
	return ReadPasskeyConfig(database.GetDB())
}

func ReadPasskeyConfig(db *gorm.DB) (*PasskeyConfigView, error) {
	view := &PasskeyConfigView{Config: PasskeySettings{Origins: []string{}, HTTPSMode: "direct"}}
	var row model.PasskeyConfig
	err := db.First(&row, 1).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal([]byte(row.Data), &view.Config); err != nil {
			return nil, err
		}
		view.Version = row.Version
	}
	// Proxy trust is the existing shared Web setting, never a parallel policy.
	var proxy model.Setting
	err = db.Where("key = ?", "trustedProxyCIDRs").First(&proxy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		view.Config.TrustedProxyCIDRs = DefaultTrustedProxyCIDRs
	} else if err != nil {
		return nil, err
	} else {
		view.Config.TrustedProxyCIDRs = proxy.Value
	}
	return view, nil
}

func NormalizePasskeySettings(cfg PasskeySettings) (PasskeySettings, map[string]string) {
	issues := map[string]string{}
	cfg.RPID = strings.TrimSpace(strings.ToLower(cfg.RPID))
	if cfg.HTTPSMode != "direct" && cfg.HTTPSMode != "proxy" {
		issues["httpsMode"] = "passkey.errors.httpsMode"
	}
	if cfg.RPID != "" {
		ascii, err := idna.Lookup.ToASCII(cfg.RPID)
		if err != nil || strings.ContainsAny(cfg.RPID, "/:@?#*") || strings.HasSuffix(cfg.RPID, ".") || net.ParseIP(cfg.RPID) != nil {
			issues["rpId"] = "passkey.errors.rpId"
		} else if err = protocol.ValidateRPID(ascii); err != nil {
			issues["rpId"] = "passkey.errors.rpId"
		} else {
			cfg.RPID = ascii
		}
	}
	origins := []string{}
	for _, raw := range cfg.Origins {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Host == "" || strings.HasSuffix(u.Host, ":") || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
			issues["origins"] = "passkey.errors.origins"
			continue
		}
		host, err := idna.Lookup.ToASCII(strings.ToLower(u.Hostname()))
		if err != nil || host != cfg.RPID || net.ParseIP(host) != nil {
			issues["origins"] = "passkey.errors.origins"
			continue
		}
		port := u.Port()
		if port != "" {
			p, err := strconv.Atoi(port)
			if err != nil || p < 1 || p > 65535 {
				issues["origins"] = "passkey.errors.origins"
				continue
			}
			port = strconv.Itoa(p)
		}
		origin := "https://" + host
		if port != "" && port != "443" {
			origin += ":" + port
		}
		if !slices.Contains(origins, origin) {
			origins = append(origins, origin)
		}
	}
	if len(cfg.Origins) > 16 {
		issues["origins"] = "passkey.errors.origins"
	}
	cfg.Origins = origins
	if cfg.Enabled && (cfg.RPID == "" || len(cfg.Origins) == 0) {
		issues["origins"] = "passkey.errors.origins"
	}
	if cfg.Enabled && cfg.RPID == "" {
		issues["rpId"] = "passkey.errors.rpId"
	}
	if (cfg.RPID == "") != (len(cfg.Origins) == 0) {
		issues["rpId"] = "passkey.errors.rpId"
	}
	proxies := []string{}
	for _, raw := range strings.Split(cfg.TrustedProxyCIDRs, ",") {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if addr, err := netip.ParseAddr(value); err == nil {
			value = addr.String()
		} else if prefix, err := netip.ParsePrefix(value); err == nil && prefix.Bits() > 0 {
			value = prefix.Masked().String()
		} else {
			issues["trustedProxyCIDRs"] = "passkey.errors.proxies"
			continue
		}
		if !slices.Contains(proxies, value) {
			proxies = append(proxies, value)
		}
	}
	if len(proxies) > 32 || (cfg.HTTPSMode == "proxy" && len(proxies) == 0) {
		issues["trustedProxyCIDRs"] = "passkey.errors.proxies"
	}
	cfg.TrustedProxyCIDRs = strings.Join(proxies, ",")
	return cfg, issues
}

// Caller holds AuthenticationStateMu and has freshly authenticated the browser user.
func (s *PasskeyConfigService) Save(cfg PasskeySettings, version int64) (*PasskeyConfigView, error) {
	return s.SaveAtEpoch(cfg, version, nil)
}

// The browser supplies the account snapshot that actually passed reauthentication.
// Its epoch is checked under a row lock, including changes from CLI processes.
func (s *PasskeyConfigService) SaveAtEpoch(cfg PasskeySettings, version int64, user *model.User) (*PasskeyConfigView, error) {
	cfg, issues := NormalizePasskeySettings(cfg)
	if len(issues) != 0 {
		return nil, errors.New("invalid passkey configuration")
	}
	if cfg.Enabled && cfg.HTTPSMode == "direct" {
		settings := &SettingService{}
		cert, certErr := settings.GetCertFile()
		key, keyErr := settings.GetKeyFile()
		if certErr != nil || keyErr != nil {
			return nil, ErrPasskeyDirectHTTPS
		}
		if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
			return nil, ErrPasskeyDirectHTTPS
		}
	}
	var result *PasskeyConfigView
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		if user != nil {
			var current model.User
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, user.Id).Error; err != nil {
				return err
			}
			if current.LoginEpoch != user.LoginEpoch {
				return ErrAuthenticationChanged
			}
		}
		current, err := ReadPasskeyConfig(tx)
		if err != nil {
			return err
		}
		if current.Version != version {
			return ErrPasskeyConfigConflict
		}
		proxyValue := cfg.TrustedProxyCIDRs
		stored := cfg
		stored.TrustedProxyCIDRs = ""
		data, err := json.Marshal(stored)
		if err != nil {
			return err
		}
		row := model.PasskeyConfig{Id: 1, Version: version + 1, Data: string(data)}
		if version == 0 {
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		} else {
			update := tx.Model(&model.PasskeyConfig{}).Where("id = 1 AND version = ?", version).Updates(map[string]any{"version": row.Version, "data": row.Data})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return ErrPasskeyConfigConflict
			}
		}
		if err := tx.Where("key = ?", "trustedProxyCIDRs").Delete(&model.Setting{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.Setting{Key: "trustedProxyCIDRs", Value: proxyValue}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.User{}).Where("1 = 1").Update("login_epoch", gorm.Expr("login_epoch + 1")).Error; err != nil {
			return err
		}
		result = &PasskeyConfigView{Config: cfg, Version: row.Version}
		return nil
	})
	return result, err
}
