package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/httpx"
)

// ErrorMapper maps domain errors to HTTP responses.
func ErrorMapper(err error) (httpx.MappedError, bool) {
	switch {
	case errors.Is(err, domain.ErrProviderUnreachable):
		return httpx.MappedError{Status: fiber.StatusBadRequest, Code: "provider_unreachable", Message: err.Error()}, true
	case errors.Is(err, domain.ErrFormatNotDetected):
		return httpx.MappedError{Status: fiber.StatusBadRequest, Code: "format_not_detected", Message: err.Error()}, true
	case errors.Is(err, domain.ErrInvalidInput):
		return httpx.MappedError{Status: fiber.StatusBadRequest, Code: "invalid_input", Message: err.Error()}, true
	case errors.Is(err, domain.ErrNotFound):
		return httpx.MappedError{Status: fiber.StatusNotFound, Code: "not_found", Message: "resource not found"}, true
	case errors.Is(err, domain.ErrConflict):
		return httpx.MappedError{Status: fiber.StatusConflict, Code: "conflict", Message: err.Error()}, true
	case errors.Is(err, domain.ErrSyncInProgress):
		return httpx.MappedError{Status: fiber.StatusConflict, Code: "sync_in_progress", Message: err.Error()}, true
	}
	return httpx.MappedError{}, false
}
