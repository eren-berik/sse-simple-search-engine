package domain

import "errors"

var (
	ErrNotFound       = errors.New("not found")
	ErrInvalidInput   = errors.New("invalid input")
	ErrSyncInProgress = errors.New("sync already in progress")
)
