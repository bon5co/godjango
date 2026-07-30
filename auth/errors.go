package auth

import "errors"

var (
	ErrUsernameRequired         = errors.New("godjango auth: username is required")
	ErrSuperuserNotStaff        = errors.New("godjango auth: superuser must be staff")
	ErrSuperuserFlag            = errors.New("godjango auth: superuser flag must be true")
	ErrUserNotFound             = errors.New("godjango auth: user not found")
	ErrUnknownPasswordAlgorithm = errors.New("godjango auth: unknown password hashing algorithm")
	ErrInvalidPasswordEncoding  = errors.New("godjango auth: invalid password encoding")
	ErrInvalidResetTokenInput   = errors.New("godjango auth: reset token requires a user and secret")
)
