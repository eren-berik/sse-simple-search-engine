package main

import (
	"context"
	"crypto/rsa"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/auth"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/config"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/http/handler"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/http/router"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/repository/postgres"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/pkg/httpx"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/pkg/jwtauth"
)

var _ auth.Store = (*postgres.Store)(nil)

// application holds the wired components and the resources that must be released on shutdown.
type application struct {
	cfg  config.Config
	log  *slog.Logger
	pool *pgxpool.Pool
	http *fiber.App
}

// boot connects to the database, loads the signing key and wires every component together.
func boot(ctx context.Context, cfg config.Config, log *slog.Logger) (*application, error) {
	key, err := loadSigningKey(cfg, log)
	if err != nil {
		return nil, err
	}

	pool, err := connectDatabase(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	app := &application{cfg: cfg, log: log, pool: pool}

	signer := jwtauth.NewSigner(jwtauth.SignerConfig{
		Key:      key,
		Issuer:   cfg.JWTIssuer,
		Audience: cfg.JWTAudience,
		TTL:      cfg.AccessTokenTTL,
	})
	verifier := jwtauth.NewVerifier(signer.KeySource(), cfg.JWTIssuer, cfg.JWTAudience[0])

	authSvc, err := auth.NewService(postgres.NewStore(pool), auth.BcryptHasher{Cost: cfg.BcryptCost}, signer,
		auth.Config{RefreshTokenTTL: cfg.RefreshTokenTTL}, log)
	if err != nil {
		app.close()
		return nil, err
	}

	app.http = router.New(router.Deps{
		Auth:     handler.NewAuthHandler(authSvc),
		Signer:   signer,
		Verifier: verifier,
		Ready:    httpx.Ready(map[string]httpx.Check{"database": pool.Ping}, nil),
		Log:      log,
	})
	return app, nil
}

func (a *application) close() {
	if a.pool != nil {
		a.pool.Close()
	}
}

// loadSigningKey reads the RSA key from the environment or a file, or generates an ephemeral one.
func loadSigningKey(cfg config.Config, log *slog.Logger) (*rsa.PrivateKey, error) {
	pemData := []byte(cfg.JWTPrivateKey)
	if len(pemData) == 0 && cfg.JWTPrivateKeyFile != "" {
		data, err := os.ReadFile(cfg.JWTPrivateKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read JWT private key: %w", err)
		}
		pemData = data
	}
	if len(pemData) > 0 {
		key, err := jwtauth.ParsePrivateKeyPEM(pemData)
		if err != nil {
			return nil, fmt.Errorf("JWT private key: %w", err)
		}
		return key, nil
	}

	log.Warn("no JWT private key configured; generating an ephemeral key (access tokens will not survive restarts)")
	return jwtauth.GenerateKey(2048)
}

// connectDatabase opens the pool, waiting up to 30s for the database, and applies migrations.
func connectDatabase(ctx context.Context, url string) (*pgxpool.Pool, error) {
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	pool, err := postgres.Connect(connectCtx, url)
	if err != nil {
		return nil, err
	}
	if err := postgres.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
