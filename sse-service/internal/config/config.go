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

	DatabaseURL string `env:"DATABASE_URL,required"`
	// RedisURL is optional; without it an in-process cache is used.
	RedisURL string        `env:"REDIS_URL"`
	CacheTTL time.Duration `env:"CACHE_TTL" envDefault:"90s"`

	ProviderTimeout    time.Duration `env:"PROVIDER_TIMEOUT" envDefault:"5s"`
	ProviderRatePerSec float64       `env:"PROVIDER_RATE_PER_SEC" envDefault:"2"`
	ProviderBurst      int           `env:"PROVIDER_BURST" envDefault:"1"`
	ProviderMaxRetries int           `env:"PROVIDER_MAX_RETRIES" envDefault:"3"`

	SyncInterval time.Duration `env:"SYNC_INTERVAL" envDefault:"10m"`

	VideoCoefficient float64 `env:"SCORE_VIDEO_COEFFICIENT" envDefault:"1.5"`
	TextCoefficient  float64 `env:"SCORE_TEXT_COEFFICIENT" envDefault:"1.0"`

	RateLimitMax    int           `env:"RATE_LIMIT_MAX" envDefault:"100"`
	RateLimitWindow time.Duration `env:"RATE_LIMIT_WINDOW" envDefault:"1m"`

	// AdminAPIKey protects admin endpoints; when empty they are disabled.
	AdminAPIKey string `env:"ADMIN_API_KEY"`
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
