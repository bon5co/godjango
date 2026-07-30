package auth

import "crypto/subtle"

const (
	SessionUserIDKey   = "_auth_user_id"
	SessionAuthHashKey = "_auth_user_hash"
)

// Session is the behavior auth needs from an SCS-backed request session.
type Session interface {
	ID() string
	Get(key string) (string, bool)
	Put(key, value string)
	Delete(key string)
	Cycle() error
	Flush() error
}

func Login(session Session, user *User, secret []byte) error {
	currentUserID, authenticated := session.Get(SessionUserIDKey)
	switch {
	case authenticated && currentUserID != user.ID:
		if err := session.Flush(); err != nil {
			return err
		}
	case !authenticated:
		if err := session.Cycle(); err != nil {
			return err
		}
	}
	session.Put(SessionUserIDKey, user.ID)
	session.Put(SessionAuthHashKey, user.SessionAuthHash(secret))
	return nil
}

func Logout(session Session) error {
	return session.Flush()
}

func SessionUserID(session Session, user *User, secret []byte) (string, bool) {
	id, ok := session.Get(SessionUserIDKey)
	if !ok || user == nil || id != user.ID {
		return "", false
	}
	storedHash, ok := session.Get(SessionAuthHashKey)
	if !ok {
		return "", false
	}
	expectedHash := user.SessionAuthHash(secret)
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(expectedHash)) != 1 {
		_ = session.Flush()
		return "", false
	}
	return id, true
}
