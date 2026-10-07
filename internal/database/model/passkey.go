package model

// PasskeyConfig is a singleton. Keeping its version and payload in one row makes
// configuration changes atomic, independently of the general settings form.
type PasskeyConfig struct {
	Id      int    `gorm:"primaryKey;autoIncrement:false"`
	Version int64  `gorm:"not null"`
	Data    string `gorm:"not null"`
}

type WebAuthnUser struct {
	Id         int    `gorm:"primaryKey;autoIncrement"`
	UserId     int    `gorm:"not null;uniqueIndex:idx_webauthn_user_rp"`
	RPID       string `gorm:"not null;uniqueIndex:idx_webauthn_user_rp;uniqueIndex:idx_webauthn_handle_rp"`
	UserHandle string `gorm:"not null;uniqueIndex:idx_webauthn_handle_rp"`
}

type PasskeyCredential struct {
	Id             int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId         int    `json:"-" gorm:"not null;index"`
	RPID           string `json:"rpId" gorm:"not null;uniqueIndex:idx_passkey_credential_rp"`
	CredentialId   string `json:"-" gorm:"not null;uniqueIndex:idx_passkey_credential_rp"`
	Name           string `json:"name" gorm:"not null"`
	CredentialData string `json:"-" gorm:"not null"`
	CreatedAt      int64  `json:"createdAt"`
	LastUsedAt     int64  `json:"lastUsedAt"`
}
