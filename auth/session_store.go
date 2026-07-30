package auth

import (
	"context"
	"errors"
	"time"
)

var ErrSessionNotFound = errors.New("godjango auth: session not found")

type StoredSession struct {
	Key       string
	Data      []byte
	ExpiresAt time.Time
}

func (store *BunStore) SaveSession(ctx context.Context, session StoredSession) error {
	return errors.New("godjango auth persistence: session save not implemented")
}

func (store *BunStore) StoredSession(ctx context.Context, key string) (StoredSession, error) {
	return StoredSession{}, errors.New("godjango auth persistence: session load not implemented")
}

func (store *BunStore) DeleteSession(ctx context.Context, key string) error {
	return errors.New("godjango auth persistence: session delete not implemented")
}

func (store *BunStore) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	return 0, errors.New("godjango auth persistence: session cleanup not implemented")
}
