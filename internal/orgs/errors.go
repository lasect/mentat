package orgs

import "errors"

var (
	ErrNotFound          = errors.New("organization resource not found")
	ErrForbidden         = errors.New("organization permission denied")
	ErrSlugTaken         = errors.New("slug already in use")
	ErrPaidPlanRequired  = errors.New("paid plan required")
	ErrInvalidStore      = errors.New("invalid analytics store")
	ErrInvalidExtension  = errors.New("invalid database extension")
	ErrInvalidConnection = errors.New("invalid database connection string")
	ErrInvalidInput      = errors.New("invalid organization input")
)
