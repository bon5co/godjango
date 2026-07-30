package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

var ErrSessionNotFound = errors.New("godjango auth: session not found")

type StoredSession struct {
	Key       string
	Data      []byte
	ExpiresAt time.Time
}

type sessionModel struct {
	bun.BaseModel `bun:"table:auth_sessions,alias:s"`
	SessionKey    string    `bun:"session_key,pk"`
	Data          []byte    `bun:"data,notnull"`
	ExpiresAt     time.Time `bun:"expires_at,notnull"`
}

func (store *BunStore) SaveSession(ctx context.Context, session StoredSession) error {
	if session.Key == "" {
		return errors.New("godjango auth: session key is required")
	}
	model := &sessionModel{
		SessionKey: session.Key,
		Data:       append([]byte(nil), session.Data...),
		ExpiresAt:  session.ExpiresAt,
	}
	_, err := store.idb.NewInsert().
		Model(model).
		On("CONFLICT (session_key) DO UPDATE").
		Set("data = EXCLUDED.data").
		Set("expires_at = EXCLUDED.expires_at").
		Exec(ctx)
	return err
}

func (store *BunStore) StoredSession(ctx context.Context, key string) (StoredSession, error) {
	model := new(sessionModel)
	err := store.idb.NewSelect().
		Model(model).
		Where("session_key = ?", key).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredSession{}, ErrSessionNotFound
	}
	if err != nil {
		return StoredSession{}, err
	}
	return StoredSession{
		Key:       model.SessionKey,
		Data:      append([]byte(nil), model.Data...),
		ExpiresAt: model.ExpiresAt,
	}, nil
}

func (store *BunStore) DeleteSession(ctx context.Context, key string) error {
	_, err := store.idb.NewDelete().
		Model((*sessionModel)(nil)).
		Where("session_key = ?", key).
		Exec(ctx)
	return err
}

func (store *BunStore) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	result, err := store.idb.NewDelete().
		Model((*sessionModel)(nil)).
		Where("expires_at <= ?", now).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
