package domain

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrInvalidInput   = errors.New("invalid input")
	ErrConflict       = errors.New("conflict")
	ErrSyncInProgress = errors.New("sync already in progress")

	// ErrProviderUnreachable and ErrFormatNotDetected are invalid input too, with their own codes
	// so clients can explain why a provider URL was rejected.
	ErrProviderUnreachable = fmt.Errorf("%w: could not fetch the provider url", ErrInvalidInput)
	ErrFormatNotDetected   = fmt.Errorf("%w: could not detect the provider format; the response matches none of the supported schemas (json, xml)", ErrInvalidInput)
)
