// Package httpx holds the HTTP conventions shared by every service: the Fiber app setup,
// JSON envelopes, error mapping, request logging and health endpoints.
package httpx

import (
	"github.com/gofiber/fiber/v2"
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

// OK writes a 200 response with the data envelope.
func OK(c *fiber.Ctx, data, meta any) error {
	return c.JSON(Data{Data: data, Meta: meta})
}

// Created writes a 201 response with the data envelope.
func Created(c *fiber.Ctx, data any) error {
	return c.Status(fiber.StatusCreated).JSON(Data{Data: data})
}

// Error writes an error envelope with the given status.
func Error(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(ErrorBody{Error: ErrorDetail{Code: code, Message: message}})
}
