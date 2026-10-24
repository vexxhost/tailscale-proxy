package endpoint

import "os"

// Settings holds the process-wide settings shared by every endpoint.
type Settings struct {
	// ControlURL is the coordination server endpoints register with, such
	// as a Headscale server. Empty uses the Tailscale default.
	ControlURL string

	// AuthKey is either an auth key or an OAuth client secret. tsnet
	// exchanges an OAuth client secret for a single-use auth key.
	AuthKey string

	// StateDir is the directory that holds one state directory per
	// endpoint, named after its hostname.
	StateDir string
}

// SettingsFromEnv reads the settings from the environment.
func SettingsFromEnv() Settings {
	authKey := os.Getenv("TS_AUTHKEY")
	if secret := os.Getenv("TS_OAUTH_CLIENT_SECRET"); secret != "" {
		authKey = secret
	}

	return Settings{
		ControlURL: os.Getenv("TS_CONTROL_URL"),
		AuthKey:    authKey,
		StateDir:   "/var/lib/tailscale-proxy",
	}
}
