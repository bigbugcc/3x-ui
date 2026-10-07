package panel

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

var errPasskeyCeremony = errors.New("passkey ceremony expired or invalid")

type PasskeyCeremony struct {
	Binding, Purpose, Name, Origin string
	UserID, TargetID               int
	Epoch, Version                 int64
	Expires                        time.Time
	Data                           *webauthn.SessionData
}

type PasskeyStore struct {
	mu   sync.Mutex
	now  func() time.Time
	rows map[string]PasskeyCeremony
}

func NewPasskeyStore() *PasskeyStore {
	return &PasskeyStore{now: time.Now, rows: map[string]PasskeyCeremony{}}
}

func (s *PasskeyStore) Put(row PasskeyCeremony, ttl time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	count := 0
	for id, old := range s.rows {
		if !now.Before(old.Expires) {
			delete(s.rows, id)
		} else if old.Binding == row.Binding {
			count++
		}
	}
	if row.Binding == "" || len(s.rows) >= 10000 || count >= 3 {
		return "", errPasskeyCeremony
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(buf)
	row.Expires = now.Add(ttl)
	s.rows[id] = row
	return id, nil
}

func (s *PasskeyStore) Take(id, binding, purpose string) (PasskeyCeremony, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	// A wrong browser cannot consume another browser's ceremony.
	if !ok || binding == "" || row.Binding != binding {
		return PasskeyCeremony{}, errPasskeyCeremony
	}
	delete(s.rows, id)
	if row.Purpose != purpose || !s.now().Before(row.Expires) {
		return PasskeyCeremony{}, errPasskeyCeremony
	}
	return row, nil
}

func (s *PasskeyStore) ClearBinding(binding string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, row := range s.rows {
		if row.Binding == binding {
			delete(s.rows, id)
		}
	}
}

func (s *PasskeyStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.rows)
}
