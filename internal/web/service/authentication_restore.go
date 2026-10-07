package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

type authenticationRestore struct {
	Version  int                 `json:"version"`
	KeepHost bool                `json:"keepHost"`
	Passkey  model.PasskeyConfig `json:"passkey"`
	Proxy    string              `json:"proxy"`
}

func authenticationRestorePath() string {
	return config.GetDBPath() + ".auth-restore"
}

// The journal lives outside the restored database and survives process crashes.
// It contains deployment policy, never passwords, TOTP secrets or credentials.
func (r authenticationRestore) prepare() error {
	if err := os.MkdirAll(filepath.Dir(authenticationRestorePath()), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(authenticationRestorePath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create authentication restore journal: %w", err)
	}
	database.AuthenticationSuspended.Store(true)
	encodeErr := json.NewEncoder(f).Encode(r)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(encodeErr, syncErr, closeErr); err != nil {
		return err
	}
	return syncAuthenticationRestoreDirectory()
}

func syncAuthenticationRestoreDirectory() error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(filepath.Dir(authenticationRestorePath()))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func (r authenticationRestore) finalize() error {
	if err := finalizeImportedAuthentication(database.GetDB(), r.KeepHost, r.Passkey, r.Proxy); err != nil {
		return err
	}
	if err := os.Remove(authenticationRestorePath()); err != nil {
		return fmt.Errorf("remove authentication restore journal: %w", err)
	}
	if err := syncAuthenticationRestoreDirectory(); err != nil {
		return err
	}
	database.AuthenticationSuspended.Store(false)
	return nil
}

// RecoverRestoredAuthentication must run before serving HTTP after every start.
// A failed recovery leaves authentication suspended and prevents server startup.
func RecoverRestoredAuthentication() error {
	AuthenticationStateMu.Lock()
	defer AuthenticationStateMu.Unlock()
	f, err := os.Open(authenticationRestorePath())
	if errors.Is(err, os.ErrNotExist) {
		if database.AuthenticationSuspended.Load() {
			return errors.New("authentication restore journal is missing while recovery is pending")
		}
		return nil
	}
	database.AuthenticationSuspended.Store(true)
	if err != nil {
		return fmt.Errorf("read authentication restore journal: %w", err)
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 128<<10))
	decoder.DisallowUnknownFields()
	var restore authenticationRestore
	if err := decoder.Decode(&restore); err != nil {
		return err
	}
	if restore.Version != 1 {
		return errors.New("unsupported authentication restore journal")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("invalid authentication restore journal")
	}
	if err := f.Close(); err != nil {
		return err
	}
	return restore.finalize()
}
