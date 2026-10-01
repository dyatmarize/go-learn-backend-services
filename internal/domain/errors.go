package domain

import "errors"

// Sentinel errors. Compare with errors.Is, never with ==.
// They describe WHAT went wrong, with no knowledge of HTTP or of PostgreSQL.
var (
	ErrNotFound           = errors.New("not found")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrForbidden          = errors.New("forbidden")
	ErrConflict           = errors.New("conflict")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrEmailTaken         = errors.New("email already registered")
	ErrAccountInactive    = errors.New("account is not active")
)
