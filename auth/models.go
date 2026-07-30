// Package auth provides GoDjangGo's default user and authorization system.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/uptrace/bun"
)

// Permission uses Django's stable "app.codename" representation.
type Permission string

type Group struct {
	Name        string
	Permissions []Permission
}

// User is the default Bun-backed authentication model.
type User struct {
	bun.BaseModel `bun:"table:auth_users,alias:u"`

	ID           string     `bun:"id,pk,type:uuid"`
	Username     string     `bun:"username,unique,notnull"`
	Email        string     `bun:"email,notnull"`
	PasswordHash string     `bun:"password_hash,notnull"`
	IsStaff      bool       `bun:"is_staff,notnull,default:false"`
	IsActive     bool       `bun:"is_active,notnull,default:true"`
	IsSuperuser  bool       `bun:"is_superuser,notnull,default:false"`
	LastLogin    *time.Time `bun:"last_login"`
	DateJoined   time.Time  `bun:"date_joined,notnull"`

	DirectPermissions []Permission `bun:"-"`
	Groups            []Group      `bun:"-"`
}

func (u *User) SetPassword(hasher *PasswordHasher, raw *string) error {
	encoded, err := hasher.Encode(raw)
	if err != nil {
		return err
	}
	u.PasswordHash = encoded
	return nil
}

func (u *User) CheckPassword(hasher *PasswordHasher, raw string) (bool, error) {
	result, err := hasher.Check(&raw, u.PasswordHash)
	if err != nil {
		return false, err
	}
	return result.OK, nil
}

func (u *User) HasUsablePassword(hasher *PasswordHasher) bool {
	return hasher.IsUsable(u.PasswordHash)
}

func (u *User) SessionAuthHash(secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(u.PasswordHash))
	return hex.EncodeToString(mac.Sum(nil))
}
