package jwtauth

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// revokedSessionPrefix is the contract between the issuer, which writes revoked sessions, and
// every verifier, which reads them: auth:revoked-session:<sid>.
const revokedSessionPrefix = "auth:revoked-session:"

// revocationTimeout bounds the lookup done for every authenticated request.
const revocationTimeout = 300 * time.Millisecond

// SessionRevocations is the list of sessions whose access tokens must be rejected before they
// expire. Entries only need to live as long as an access token, so the list stays small.
//
// Lookups fail open: if Redis is unreachable the token is accepted (and the failure logged),
// because rejecting every request would turn a cache outage into a full outage. The exposure is
// bounded by the access token lifetime.
type SessionRevocations struct {
	rdb *redis.Client
	log *slog.Logger
}

func NewSessionRevocations(rdb *redis.Client, log *slog.Logger) *SessionRevocations {
	return &SessionRevocations{rdb: rdb, log: log}
}

// IsRevoked implements RevocationChecker.
func (r *SessionRevocations) IsRevoked(ctx context.Context, sessionID string) bool {
	ctx, cancel := context.WithTimeout(ctx, revocationTimeout)
	defer cancel()
	n, err := r.rdb.Exists(ctx, revokedSessionPrefix+sessionID).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		r.log.WarnContext(ctx, "session revocation lookup failed; accepting the token", "error", err)
		return false
	}
	return n > 0
}
