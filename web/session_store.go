package web

import (
	"context"
	"errors"
	"time"

	"github.com/bon5co/godjango/auth"
)

type PersistentSessionStore interface {
	SaveSession(context.Context, auth.StoredSession) error
	StoredSession(context.Context, string) (auth.StoredSession, error)
	DeleteSession(context.Context, string) error
}

// SessionStore adapts GoDjangGo's auth session persistence to the mature SCS
// session manager.
type SessionStore struct {
	store PersistentSessionStore
	now   func() time.Time
}

func NewSessionStore(store PersistentSessionStore) (*SessionStore, error) {
	if store == nil {
		return nil, errors.New("godjango web: persistent session store is required")
	}
	return &SessionStore{store: store, now: time.Now}, nil
}

func (store *SessionStore) Delete(token string) error {
	return store.DeleteCtx(context.Background(), token)
}

func (store *SessionStore) Find(token string) ([]byte, bool, error) {
	return store.FindCtx(context.Background(), token)
}

func (store *SessionStore) Commit(token string, data []byte, expiry time.Time) error {
	return store.CommitCtx(context.Background(), token, data, expiry)
}

func (store *SessionStore) DeleteCtx(ctx context.Context, token string) error {
	return store.store.DeleteSession(ctx, token)
}

func (store *SessionStore) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	session, err := store.store.StoredSession(ctx, token)
	if errors.Is(err, auth.ErrSessionNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !session.ExpiresAt.After(store.now().UTC()) {
		if err := store.store.DeleteSession(ctx, token); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	return append([]byte(nil), session.Data...), true, nil
}

func (store *SessionStore) CommitCtx(
	ctx context.Context,
	token string,
	data []byte,
	expiry time.Time,
) error {
	return store.store.SaveSession(ctx, auth.StoredSession{
		Key:       token,
		Data:      append([]byte(nil), data...),
		ExpiresAt: expiry.UTC(),
	})
}
