// Package response defines the JSON envelopes returned by the API.
package response

import (
	"errors"
	"log/slog"

	"github.com/gofiber/fiber/v2"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
)

// ErrorBody is the error envelope: {"error": {"code": "...", "message": "..."}}.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Data wraps a successful payload: {"data": ..., "meta": ...}.
type Data struct {
	Data any `json:"data"`
	Meta any `json:"meta,omitempty"`
}

func OK(c *fiber.Ctx, data, meta any) error {
	return c.JSON(Data{Data: data, Meta: meta})
}

func Error(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(ErrorBody{Error: ErrorDetail{Code: code, Message: message}})
}

// ErrorHandler maps domain and Fiber errors onto HTTP responses. Unexpected errors are logged
// and reported without internal details.
func ErrorHandler(log *slog.Logger) fiber.ErrorHandler {
	return func(c *fiber.Ctx, err error) error {
		var fe *fiber.Error
		switch {
		case errors.Is(err, domain.ErrInvalidInput):
			return Error(c, fiber.StatusBadRequest, "invalid_input", err.Error())
		case errors.Is(err, domain.ErrNotFound):
			return Error(c, fiber.StatusNotFound, "not_found", "resource not found")
		case errors.Is(err, domain.ErrConflict):
			return Error(c, fiber.StatusConflict, "conflict", err.Error())
		case errors.Is(err, domain.ErrSyncInProgress):
			return Error(c, fiber.StatusConflict, "sync_in_progress", err.Error())
		case errors.As(err, &fe):
			return Error(c, fe.Code, codeFor(fe.Code), fe.Message)
		default:
			log.ErrorContext(c.UserContext(), "unhandled error",
				"error", err, "method", c.Method(), "path", c.Path(), "request_id", c.Locals("requestid"))
			return Error(c, fiber.StatusInternalServerError, "internal_error", "internal server error")
		}
	}
}

func codeFor(status int) string {
	switch status {
	case fiber.StatusNotFound:
		return "not_found"
	case fiber.StatusMethodNotAllowed:
		return "method_not_allowed"
	case fiber.StatusUnauthorized:
		return "unauthorized"
	case fiber.StatusForbidden:
		return "forbidden"
	case fiber.StatusTooManyRequests:
		return "rate_limited"
	case fiber.StatusBadRequest:
		return "bad_request"
	default:
		if status >= 500 {
			return "internal_error"
		}
		return "error"
	}
}
