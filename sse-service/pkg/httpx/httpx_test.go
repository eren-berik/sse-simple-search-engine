package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errDomain = errors.New("domain failure")

func newTestApp() *fiber.App {
	app := NewApp(AppConfig{
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ErrorMappers: []ErrorMapper{func(err error) (MappedError, bool) {
			if errors.Is(err, errDomain) {
				return MappedError{Status: http.StatusConflict, Code: "domain", Message: "mapped"}, true
			}
			return MappedError{}, false
		}},
	})
	app.Get("/mapped", func(*fiber.Ctx) error { return errDomain })
	app.Get("/fiber", func(*fiber.Ctx) error { return fiber.ErrForbidden })
	app.Get("/unknown", func(*fiber.Ctx) error { return errors.New("db password leaked in message") })
	app.Get("/panic", func(*fiber.Ctx) error { panic("boom") })
	app.Get("/ready", Ready(
		map[string]Check{"db": func(context.Context) error { return nil }},
		map[string]Check{"cache": func(context.Context) error { return errors.New("down") }},
	))
	return app
}

func get(t *testing.T, app *fiber.App, path string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil), -1)
	require.NoError(t, err)
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp, body
}

func errorOf(body map[string]any) (string, string) {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	msg, _ := e["message"].(string)
	return code, msg
}

func TestErrorHandler(t *testing.T) {
	app := newTestApp()

	resp, body := get(t, app, "/mapped")
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	code, msg := errorOf(body)
	assert.Equal(t, "domain", code)
	assert.Equal(t, "mapped", msg)

	resp, body = get(t, app, "/fiber")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	code, _ = errorOf(body)
	assert.Equal(t, "forbidden", code)

	resp, body = get(t, app, "/unknown")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	_, msg = errorOf(body)
	assert.NotContains(t, msg, "password", "internal details are not exposed")

	resp, _ = get(t, app, "/panic")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	resp, body = get(t, app, "/missing")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	code, _ = errorOf(body)
	assert.Equal(t, "not_found", code)
}

func TestRequestIDIsPropagated(t *testing.T) {
	app := newTestApp()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	req.Header.Set(HeaderRequestID, "abc-123")

	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	_ = resp.Body.Close()

	assert.Equal(t, "abc-123", resp.Header.Get(HeaderRequestID))
}

func TestReadyDegraded(t *testing.T) {
	resp, body := get(t, newTestApp(), "/ready")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "degraded", body["status"])
}
