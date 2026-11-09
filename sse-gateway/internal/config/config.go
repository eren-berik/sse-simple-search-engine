// Package config loads the gateway settings from environment variables.
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

	IAMURL          string        `env:"IAM_URL,required"`
	SSEURL          string        `env:"SSE_URL,required"`
	UpstreamTimeout time.Duration `env:"UPSTREAM_TIMEOUT" envDefault:"30s"`

	// RedisURL is optional; without it rate limit counters are kept per instance.
	RedisURL string `env:"REDIS_URL"`

	RateLimitMax        int           `env:"RATE_LIMIT_MAX" envDefault:"100"`
	RateLimitWindow     time.Duration `env:"RATE_LIMIT_WINDOW" envDefault:"1m"`
	AuthRateLimitMax    int           `env:"AUTH_RATE_LIMIT_MAX" envDefault:"10"`
	AuthRateLimitWindow time.Duration `env:"AUTH_RATE_LIMIT_WINDOW" envDefault:"1m"`
}

func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}
