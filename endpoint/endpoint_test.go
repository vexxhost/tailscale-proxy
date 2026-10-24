package endpoint

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
	"tailscale.com/net/netns"
	"tailscale.com/tstest/integration"
	"tailscale.com/tstest/integration/testcontrol"
	"tailscale.com/types/logger"
)

func TestAuthKey(t *testing.T) {
	notEphemeral := false

	tests := []struct {
		name     string
		endpoint Endpoint
		settings Settings
		want     string
	}{
		{
			name:     "auth key is passed through",
			settings: Settings{AuthKey: "tskey-auth-abc", ControlURL: "https://hs.example.com"},
			want:     "tskey-auth-abc",
		},
		{
			name:     "OAuth secret mints ephemeral preauthorized keys by default",
			settings: Settings{AuthKey: "tskey-client-abc"},
			want:     "tskey-client-abc?ephemeral=true&preauthorized=true",
		},
		{
			name:     "OAuth secret follows the endpoint ephemeral setting",
			endpoint: Endpoint{Ephemeral: &notEphemeral},
			settings: Settings{AuthKey: "tskey-client-abc?ephemeral=true"},
			want:     "tskey-client-abc?ephemeral=false&preauthorized=true",
		},
		{
			name:     "OAuth secret uses the control URL as base URL",
			settings: Settings{AuthKey: "tskey-client-abc", ControlURL: "https://hs.example.com/"},
			want:     "tskey-client-abc?baseURL=https%3A%2F%2Fhs.example.com&ephemeral=true&preauthorized=true",
		},
		{
			name:     "OAuth secret keeps its own base URL and preauthorized",
			settings: Settings{AuthKey: "tskey-client-abc?baseURL=https://api.example.com&preauthorized=false", ControlURL: "https://hs.example.com"},
			want:     "tskey-client-abc?baseURL=https%3A%2F%2Fapi.example.com&ephemeral=true&preauthorized=false",
		},
		{
			name:     "Headscale OAuth secret is exchanged as a Tailscale one",
			settings: Settings{AuthKey: "hskey-client-abc", ControlURL: "https://hs.example.com"},
			want:     "tskey-client-abc?baseURL=https%3A%2F%2Fhs.example.com&ephemeral=true&preauthorized=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.endpoint.authKey(tt.settings)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("authKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthKeyInvalidAttributes(t *testing.T) {
	ep := Endpoint{}
	if _, err := ep.authKey(Settings{AuthKey: "tskey-client-abc?ephemeral=%zz"}); err == nil {
		t.Fatal("authKey() succeeded with invalid attributes")
	}
}

// TestServerRegistersWithControlServer starts an endpoint against a local
// control server that, like Headscale, also serves the OAuth and key API
// under /api/v2. The control server only accepts the auth key minted through
// that API, so registering proves the endpoint used the custom control server
// for both.
func TestServerRegistersWithControlServer(t *testing.T) {
	netns.SetEnabled(false)
	t.Cleanup(func() { netns.SetEnabled(true) })

	const mintedKey = "tskey-auth-minted"

	type keyRequest struct {
		Capabilities struct {
			Devices struct {
				Create struct {
					Reusable      bool     `json:"reusable"`
					Ephemeral     bool     `json:"ephemeral"`
					Preauthorized bool     `json:"preauthorized"`
					Tags          []string `json:"tags"`
				} `json:"create"`
			} `json:"devices"`
		} `json:"capabilities"`
	}

	var (
		mu           sync.Mutex
		clientSecret string
		keyReq       *keyRequest
	)

	control := &testcontrol.Server{
		DERPMap:        integration.RunDERPAndSTUN(t, logger.Discard, "127.0.0.1"),
		RequireAuthKey: mintedKey,
		Logf:           logger.Discard,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_, secret, ok := r.BasicAuth()
		if !ok {
			secret = r.PostFormValue("client_secret")
		}
		mu.Lock()
		clientSecret = secret
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("POST /api/v2/tailnet/-/keys", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		req := &keyRequest{}
		if err := json.NewDecoder(r.Body).Decode(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		keyReq = req
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "k1", "key": mintedKey})
	})
	mux.Handle("/", control)

	control.HTTPTestServer = httptest.NewUnstartedServer(mux)
	control.HTTPTestServer.Start()
	t.Cleanup(control.HTTPTestServer.Close)

	var ep Endpoint
	err := yaml.Unmarshal([]byte(`
hostname: bmc-example-server
ip: 127.0.0.1
ephemeral: false
tags:
  - bmc
`), &ep)
	if err != nil {
		t.Fatal(err)
	}

	srv, err := ep.server(Settings{
		ControlURL: control.HTTPTestServer.URL,
		AuthKey:    "hskey-client-abc-secret",
		StateDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })

	if srv.Ephemeral {
		t.Error("server is ephemeral, want persistent")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	status, err := srv.Up(ctx)
	if err != nil {
		t.Fatalf("Up() = %v", err)
	}
	if status.Self.HostName != ep.Hostname {
		t.Errorf("registered hostname = %q, want %q", status.Self.HostName, ep.Hostname)
	}

	mu.Lock()
	defer mu.Unlock()

	if clientSecret != "tskey-client-abc-secret" {
		t.Errorf("OAuth client secret = %q, want %q", clientSecret, "tskey-client-abc-secret")
	}
	if keyReq == nil {
		t.Fatal("no auth key was minted")
	}
	create := keyReq.Capabilities.Devices.Create
	if create.Reusable || create.Ephemeral || !create.Preauthorized {
		t.Errorf("minted key reusable=%v ephemeral=%v preauthorized=%v, want false false true",
			create.Reusable, create.Ephemeral, create.Preauthorized)
	}
	if !slices.Equal(create.Tags, []string{"tag:bmc"}) {
		t.Errorf("minted key tags = %q, want %q", create.Tags, []string{"tag:bmc"})
	}
}
