// Package config loads the identity service settings from environment variables.
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

	// JWTPrivateKey is a PEM encoded RSA key (inline or via JWTPrivateKeyFile). When neither is set,
	// an ephemeral key is generated: fine for development, but issued access tokens stop
	// validating after a restart (refresh tokens keep working).
	JWTPrivateKey     string        `env:"JWT_PRIVATE_KEY"`
	JWTPrivateKeyFile string        `env:"JWT_PRIVATE_KEY_FILE"`
	JWTIssuer         string        `env:"JWT_ISSUER" envDefault:"iam-service"`
	JWTAudience       []string      `env:"JWT_AUDIENCE" envDefault:"sse"`
	AccessTokenTTL    time.Duration `env:"ACCESS_TOKEN_TTL" envDefault:"15m"`
	RefreshTokenTTL   time.Duration `env:"REFRESH_TOKEN_TTL" envDefault:"168h"`

	BcryptCost int `env:"BCRYPT_COST" envDefault:"12"`

	// RedisURL holds the list of revoked sessions shared with the verifiers. Optional: without
	// it, logout still revokes refresh tokens, but access tokens stay valid until they expire.
	RedisURL string `env:"REDIS_URL"`
}

func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	if cfg.AccessTokenTTL <= 0 || cfg.RefreshTokenTTL <= cfg.AccessTokenTTL {
		return Config{}, fmt.Errorf("load config: token TTLs must be positive and REFRESH_TOKEN_TTL longer than ACCESS_TOKEN_TTL")
	}
	return cfg, nil
}
