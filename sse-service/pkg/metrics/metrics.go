// Package metrics exposes Prometheus metrics: a registry with runtime metrics, HTTP request
// metrics for Fiber, and a separate listener so /metrics is never served on the public port.
package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRegistry returns a registry with the Go runtime and process collectors.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return reg
}

// HTTP records the count and latency of every request. Requests are labelled with the route
// pattern (e.g. /api/v1/contents/:id), not the raw path, to keep the number of series bounded.
// It must run outside the request logger, which turns errors into responses, so that the
// recorded status is the one sent to the client.
func HTTP(reg prometheus.Registerer) fiber.Handler {
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP requests by method, route and status.",
	}, []string{"method", "route", "status"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency by method and route.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"method", "route"})
	reg.MustRegister(requests, duration)

	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		route := c.Route().Path
		if route == "" {
			route = "unmatched"
		}
		method := c.Method()
		requests.WithLabelValues(method, route, strconv.Itoa(c.Response().StatusCode())).Inc()
		duration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
		return err
	}
}

// Server serves /metrics for reg on its own address until ctx is cancelled.
type Server struct {
	srv *http.Server
	log *slog.Logger
}

func NewServer(addr string, reg *prometheus.Registry, log *slog.Logger) *Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	return &Server{
		srv: &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second},
		log: log,
	}
}

// Run listens until ctx is cancelled. A failing metrics listener is logged, not fatal:
// the service keeps serving traffic without it.
func (s *Server) Run(ctx context.Context) {
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
	}()
	s.log.Info("metrics server listening", "addr", s.srv.Addr)
	if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.log.Error("metrics server", "error", err)
	}
}
