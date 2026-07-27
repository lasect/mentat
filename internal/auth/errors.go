package auth

import "errors"

var (
	ErrInvalidInput         = errors.New("invalid input")
	ErrEmailTaken           = errors.New("email already registered")
	ErrEmailNotAvailable    = errors.New("email not available")
	ErrInvalidCredentials   = errors.New("invalid email or password")
	ErrInvalidToken         = errors.New("invalid token")
	ErrInvalidSigningMethod = errors.New("invalid token signing method")
	ErrSessionExpired       = errors.New("session expired")
	ErrSessionRevoked       = errors.New("session revoked")
	ErrUserDisabled         = errors.New("user disabled")
	ErrVerifiedEmailNeeded  = errors.New("verified provider email required")
	ErrAccountLinkRequired  = errors.New("account link required")
	ErrIdentityConflict     = errors.New("provider identity already linked")
)
