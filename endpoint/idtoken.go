package endpoint

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"tailscale.com/client/local"
)

const (
	// idTokenMinValidity is how long a cached ID token must stay valid to be
	// handed out again: enough for tsnet to exchange it.
	idTokenMinValidity = time.Minute

	// idTokenRetryFor bounds how long a token is retried while tailscaled is
	// not ready yet, such as right after boot.
	idTokenRetryFor = 2 * time.Minute
)

// NewTailscaledIDToken returns an ID token source asking the tailscaled at
// socket (the platform default when empty) for a token about its own node,
// the call `tailscale id-token` makes. The control server signs it, so it
// needs a control server that issues them, such as Headscale.
//
// One token serves every endpoint while it stays valid, so starting many
// endpoints asks tailscaled once.
func NewTailscaledIDToken(socket string) func(ctx context.Context, audience string) (string, error) {
	lc := &local.Client{Socket: socket}

	return newCachedIDToken(func(ctx context.Context, audience string) (string, error) {
		resp, err := lc.IDToken(ctx, audience)
		if err != nil {
			return "", err
		}

		return resp.IDToken, nil
	}, time.Now)
}

func newCachedIDToken(fetch func(ctx context.Context, audience string) (string, error), now func() time.Time) func(ctx context.Context, audience string) (string, error) {
	var (
		mu     sync.Mutex
		tokens = map[string]string{}
	)

	return func(ctx context.Context, audience string) (string, error) {
		mu.Lock()
		defer mu.Unlock()

		if tok, ok := tokens[audience]; ok {
			if exp, err := tokenExpiry(tok); err == nil && exp.Sub(now()) > idTokenMinValidity {
				return tok, nil
			}
		}

		ctx, cancel := context.WithTimeout(ctx, idTokenRetryFor)
		defer cancel()

		for delay := time.Second; ; delay = min(2*delay, 15*time.Second) {
			tok, err := fetch(ctx, audience)
			if err == nil {
				tokens[audience] = tok
				return tok, nil
			}

			log.Printf("requesting an ID token from tailscaled: %v; retrying in %v", err, delay)

			select {
			case <-ctx.Done():
				return "", fmt.Errorf("requesting an ID token from tailscaled: %w", err)
			case <-time.After(delay):
			}
		}
	}
}

// tokenExpiry reads a JWT's exp claim without verifying it: it only decides
// whether a token we were just handed is worth reusing.
func tokenExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, errors.New("not a JWT")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, err
	}

	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, err
	}

	return time.Unix(claims.Exp, 0), nil
}
