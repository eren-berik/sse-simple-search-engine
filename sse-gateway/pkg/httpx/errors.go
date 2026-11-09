package httpx

import (
	"errors"
	"log/slog"

	"github.com/gofiber/fiber/v2"
)

// MappedError is the HTTP representation of an application error.
type MappedError struct {
	Status  int
	Code    string
	Message string
}

// ErrorMapper translates application errors into HTTP responses; ok is false for errors it does not know.
type ErrorMapper func(err error) (m MappedError, ok bool)

// ErrorHandler returns a Fiber error handler that tries each mapper in order, then handles
// *fiber.Error values. Anything else is logged and reported as a 500 without internal details.
func ErrorHandler(log *slog.Logger, mappers ...ErrorMapper) fiber.ErrorHandler {
	return func(c *fiber.Ctx, err error) error {
		for _, m := range mappers {
			if mapped, ok := m(err); ok {
				return Error(c, mapped.Status, mapped.Code, mapped.Message)
			}
		}

		var fe *fiber.Error
		if errors.As(err, &fe) {
			return Error(c, fe.Code, CodeForStatus(fe.Code), fe.Message)
		}

		log.ErrorContext(c.UserContext(), "unhandled error",
			"error", err, "method", c.Method(), "path", c.Path(), "request_id", c.Locals("requestid"))
		return Error(c, fiber.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// CodeForStatus returns the machine-readable error code used for a bare HTTP status.
func CodeForStatus(status int) string {
	switch status {
	case fiber.StatusBadRequest:
		return "bad_request"
	case fiber.StatusUnauthorized:
		return "unauthorized"
	case fiber.StatusForbidden:
		return "forbidden"
	case fiber.StatusNotFound:
		return "not_found"
	case fiber.StatusMethodNotAllowed:
		return "method_not_allowed"
	case fiber.StatusConflict:
		return "conflict"
	case fiber.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case fiber.StatusTooManyRequests:
		return "rate_limited"
	case fiber.StatusBadGateway:
		return "bad_gateway"
	case fiber.StatusServiceUnavailable:
		return "service_unavailable"
	case fiber.StatusGatewayTimeout:
		return "gateway_timeout"
	default:
		if status >= 500 {
			return "internal_error"
		}
		return "error"
	}
}
