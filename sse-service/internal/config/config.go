// Package config loads application settings from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	HTTPAddr string     `env:"HTTP_ADDR" envDefault:":8080"`
	LogLevel slog.Level `env:"LOG_LEVEL" envDefault:"info"`
	// MetricsAddr serves Prometheus metrics on a separate, unpublished port.
	MetricsAddr string `env:"METRICS_ADDR" envDefault:":9100"`

	DatabaseURL string `env:"DATABASE_URL,required"`
	// RedisURL is optional; without it an in-process cache is used.
	RedisURL string        `env:"REDIS_URL"`
	CacheTTL time.Duration `env:"CACHE_TTL" envDefault:"90s"`

	ProviderTimeout    time.Duration `env:"PROVIDER_TIMEOUT" envDefault:"5s"`
	ProviderRatePerSec float64       `env:"PROVIDER_RATE_PER_SEC" envDefault:"2"`
	ProviderBurst      int           `env:"PROVIDER_BURST" envDefault:"1"`
	ProviderMaxRetries int           `env:"PROVIDER_MAX_RETRIES" envDefault:"3"`
	// A provider's circuit opens after this many consecutive failed requests and stays open
	// for the timeout; longer than SYNC_INTERVAL so at least one scheduled sync skips it.
	ProviderBreakerFailureThreshold uint32        `env:"PROVIDER_BREAKER_FAILURE_THRESHOLD" envDefault:"5"`
	ProviderBreakerOpenTimeout      time.Duration `env:"PROVIDER_BREAKER_OPEN_TIMEOUT" envDefault:"15m"`

	// JWKSURL is the identity service's public key set used to verify access tokens.
	JWKSURL     string `env:"JWKS_URL,required"`
	JWTIssuer   string `env:"JWT_ISSUER" envDefault:"iam-service"`
	JWTAudience string `env:"JWT_AUDIENCE" envDefault:"sse"`

	SyncInterval time.Duration `env:"SYNC_INTERVAL" envDefault:"10m"`

	VideoCoefficient float64 `env:"SCORE_VIDEO_COEFFICIENT" envDefault:"1.5"`
	TextCoefficient  float64 `env:"SCORE_TEXT_COEFFICIENT" envDefault:"1.0"`
}

func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	if cfg.SyncInterval <= 0 {
		return Config{}, fmt.Errorf("load config: SYNC_INTERVAL must be positive")
	}
	return cfg, nil
}
