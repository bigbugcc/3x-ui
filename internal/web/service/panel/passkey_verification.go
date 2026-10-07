package panel

import (
	"bytes"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func (s *PasskeyService) BeginRegistration(user *model.User, cfg service.PasskeySettings) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	rows, err := s.List(user.Id)
	if err != nil {
		return nil, nil, err
	}
	if len(rows) >= MaxPasskeys {
		return nil, nil, ErrPasskeyLimit
	}
	adapter, err := s.loadUser(passkeyDB(), user, cfg.RPID, true)
	if err != nil {
		return nil, nil, err
	}
	w, err := NewPasskeyWebAuthn(cfg)
	if err != nil {
		return nil, nil, err
	}
	return w.BeginRegistration(adapter,
		webauthn.WithExclusions(webauthn.Credentials(adapter.Credentials).CredentialDescriptors()),
		webauthn.WithExtensions(webauthn.WithExtensionCredProps()),
	)
}

func (s *PasskeyService) FinishRegistration(user *model.User, cfg *service.PasskeyConfigView, ceremony PasskeyCeremony, response []byte) error {
	if ceremony.UserID != user.Id || ceremony.Epoch != user.LoginEpoch || ceremony.Version != cfg.Version || ceremony.Data == nil {
		return ErrPasskeyRejected
	}
	adapter, err := s.loadUser(passkeyDB(), user, cfg.Config.RPID, false)
	if err != nil {
		return err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(response))
	if err != nil || parsed.Response.CollectedClientData.CrossOrigin || parsed.Response.CollectedClientData.Origin != ceremony.Origin {
		return ErrPasskeyRejected
	}
	w, err := NewPasskeyWebAuthn(cfg.Config)
	if err != nil {
		return err
	}
	credential, err := w.CreateCredential(adapter, *ceremony.Data, parsed)
	if err != nil {
		return ErrPasskeyRejected
	}
	// credProps is unsigned compatibility output; required resident-key options
	// remain enforced by clients, while the signed response establishes identity.
	if props := parsed.ClientExtensionResults.CredProps; props != nil && props.RK != nil && !*props.RK {
		return ErrPasskeyRejected
	}
	return s.Register(user, cfg, ceremony.Name, credential)
}

func (s *PasskeyService) BeginLogin(cfg service.PasskeySettings) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	w, err := NewPasskeyWebAuthn(cfg)
	if err != nil {
		return nil, nil, err
	}
	return w.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
}

type PasskeyLoginResult struct {
	User       *model.User
	Credential *webauthn.Credential
	RecordID   int
}

func (s *PasskeyService) FinishLogin(cfg *service.PasskeyConfigView, ceremony PasskeyCeremony, response []byte, allowUser func(*model.User) bool) (*PasskeyLoginResult, error) {
	if ceremony.Version != cfg.Version || ceremony.Data == nil {
		return nil, ErrPasskeyRejected
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(response))
	if err != nil || parsed.Response.CollectedClientData.CrossOrigin || parsed.Response.CollectedClientData.Origin != ceremony.Origin {
		return nil, ErrPasskeyRejected
	}
	w, err := NewPasskeyWebAuthn(cfg.Config)
	if err != nil {
		return nil, err
	}
	var original *model.PasskeyCredential
	var adapter *PasskeyUser
	handler := func(rawID, handle []byte) (webauthn.User, error) {
		var err error
		adapter, original, err = s.Discover(rawID, handle, cfg.Config.RPID)
		if err != nil {
			return nil, err
		}
		if !allowUser(adapter.User) {
			return nil, ErrPasskeyRejected
		}
		return adapter, nil
	}
	_, credential, err := w.ValidatePasskeyLogin(handler, *ceremony.Data, parsed)
	var result *PasskeyLoginResult
	if adapter != nil {
		result = &PasskeyLoginResult{User: adapter.User}
	}
	if err != nil {
		return result, ErrPasskeyRejected
	}
	if err := s.RecordLogin(adapter.User, cfg.Version, original, credential); err != nil {
		return result, err
	}
	result.Credential = credential
	result.RecordID = original.Id
	return result, nil
}
