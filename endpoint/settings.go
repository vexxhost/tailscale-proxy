package endpoint

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Settings holds the process-wide settings shared by every endpoint.
type Settings struct {
	// ControlURL is the coordination server endpoints register with, such
	// as a Headscale server. Empty uses the Tailscale default.
	ControlURL string

	// AuthKey is either an auth key or an OAuth client secret. tsnet
	// exchanges an OAuth client secret for a single-use auth key.
	AuthKey string

	// ClientID is a federated identity (workload identity federation) to
	// register with instead of AuthKey: tsnet exchanges an ID token for it
	// and mints a single-use auth key carrying the endpoint's tags.
	ClientID string

	// Audience is the audience of the ID token exchanged for ClientID.
	// Empty uses Headscale's default for a federated identity,
	// "<control URL host>/<client ID>".
	Audience string

	// IDToken returns an ID token for audience. Empty asks the tailscaled
	// running on this host for one about its own node (tailscale id-token),
	// so a host already in the tailnet registers endpoints with no secret.
	IDToken func(ctx context.Context, audience string) (string, error)

	// StateDir is the directory that holds one state directory per
	// endpoint, named after its hostname.
	StateDir string
}

// SettingsFromEnv reads the settings from the environment.
//
// The workload identity variables are not tsnet's TS_CLIENT_ID and
// TS_AUDIENCE: tsnet reads those itself, and refuses an ID token and an
// audience together.
func SettingsFromEnv() Settings {
	authKey := os.Getenv("TS_AUTHKEY")
	if secret := os.Getenv("TS_OAUTH_CLIENT_SECRET"); secret != "" {
		authKey = secret
	}

	return Settings{
		ControlURL: os.Getenv("TS_CONTROL_URL"),
		AuthKey:    authKey,
		ClientID:   os.Getenv("TS_FEDERATED_CLIENT_ID"),
		Audience:   os.Getenv("TS_FEDERATED_AUDIENCE"),
		IDToken:    NewTailscaledIDToken(os.Getenv("TS_TAILSCALED_SOCKET")),
		StateDir:   "/var/lib/tailscale-proxy",
	}
}

// Validate reports settings that cannot work together.
func (s Settings) Validate() error {
	if s.ClientID == "" {
		return nil
	}

	if s.AuthKey != "" {
		return errors.New("set either an auth key or OAuth client secret, or a federated client ID, not both")
	}

	if _, err := s.audience(); err != nil {
		return err
	}

	if s.IDToken == nil {
		return errors.New("a federated client ID needs an ID token source")
	}

	return nil
}

// audience is the audience of the ID token exchanged for the client ID.
func (s Settings) audience() (string, error) {
	if s.Audience != "" {
		return s.Audience, nil
	}

	u, err := url.Parse(s.ControlURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("set TS_FEDERATED_AUDIENCE, or TS_CONTROL_URL to derive it from: %q has no host", s.ControlURL)
	}

	clientID, _, _ := strings.Cut(s.ClientID, "?")

	return u.Host + "/" + clientID, nil
}
