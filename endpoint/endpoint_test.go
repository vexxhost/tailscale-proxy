package endpoint

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
	"tailscale.com/ipn/ipnstate"
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

// keyRequest is the body of the key API's create call.
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

const mintedKey = "tskey-auth-minted"

// startControl starts a local control server that, like Headscale, also
// serves the key API under /api/v2. It only accepts the auth key minted
// through that API, with the access token "access-token" that the token
// handlers (registered on tokenRoutes) hand out. It returns the server URL
// and the last key request.
func startControl(t *testing.T, tokenRoutes map[string]http.HandlerFunc) (string, func() *keyRequest) {
	t.Helper()

	netns.SetEnabled(false)
	t.Cleanup(func() { netns.SetEnabled(true) })

	var (
		mu     sync.Mutex
		keyReq *keyRequest
	)

	control := &testcontrol.Server{
		DERPMap:        integration.RunDERPAndSTUN(t, logger.Discard, "127.0.0.1"),
		RequireAuthKey: mintedKey,
		Logf:           logger.Discard,
	}

	mux := http.NewServeMux()
	for route, h := range tokenRoutes {
		mux.HandleFunc(route, h)
	}
	mux.HandleFunc("POST /api/v2/tailnet/-/keys", func(w http.ResponseWriter, r *http.Request) {
		// The OAuth path sends the access token as a bearer token, the
		// workload identity path as the basic auth user, as Tailscale's API
		// (and Headscale) accept.
		user, _, basic := r.BasicAuth()
		if r.Header.Get("Authorization") != "Bearer access-token" && (!basic || user != "access-token") {
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

	return control.HTTPTestServer.URL, func() *keyRequest {
		mu.Lock()
		defer mu.Unlock()
		return keyReq
	}
}

func writeAccessToken(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token": "access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
	})
}

// testEndpoint is a persistent endpoint tagged bmc.
func testEndpoint(t *testing.T) Endpoint {
	t.Helper()

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

	return ep
}

// registerAndCheckKey starts srv, waits until it is registered, and checks
// the auth key it registered with was minted single-use, persistent,
// preauthorized and tagged bmc.
func registerAndCheckKey(t *testing.T, ep Endpoint, srv interface {
	Up(context.Context) (*ipnstate.Status, error)
	Close() error
}, keyReq func() *keyRequest) {
	t.Helper()
	t.Cleanup(func() { srv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	status, err := srv.Up(ctx)
	if err != nil {
		t.Fatalf("Up() = %v", err)
	}
	if status.Self.HostName != ep.Hostname {
		t.Errorf("registered hostname = %q, want %q", status.Self.HostName, ep.Hostname)
	}

	req := keyReq()
	if req == nil {
		t.Fatal("no auth key was minted")
	}
	create := req.Capabilities.Devices.Create
	if create.Reusable || create.Ephemeral || !create.Preauthorized {
		t.Errorf("minted key reusable=%v ephemeral=%v preauthorized=%v, want false false true",
			create.Reusable, create.Ephemeral, create.Preauthorized)
	}
	if !slices.Equal(create.Tags, []string{"tag:bmc"}) {
		t.Errorf("minted key tags = %q, want %q", create.Tags, []string{"tag:bmc"})
	}
}

// TestServerRegistersWithControlServer: an OAuth client secret is exchanged
// at the custom control server, so registering proves the endpoint used it
// for both the key API and registration.
func TestServerRegistersWithControlServer(t *testing.T) {
	var (
		mu           sync.Mutex
		clientSecret string
	)

	url, keyReq := startControl(t, map[string]http.HandlerFunc{
		"POST /api/v2/oauth/token": func(w http.ResponseWriter, r *http.Request) {
			_, secret, ok := r.BasicAuth()
			if !ok {
				secret = r.PostFormValue("client_secret")
			}
			mu.Lock()
			clientSecret = secret
			mu.Unlock()
			writeAccessToken(w)
		},
	})

	ep := testEndpoint(t)

	srv, err := ep.server(Settings{
		ControlURL: url,
		AuthKey:    "hskey-client-abc-secret",
		StateDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if srv.Ephemeral {
		t.Error("server is ephemeral, want persistent")
	}

	registerAndCheckKey(t, ep, srv, keyReq)

	mu.Lock()
	defer mu.Unlock()

	if clientSecret != "tskey-client-abc-secret" {
		t.Errorf("OAuth client secret = %q, want %q", clientSecret, "tskey-client-abc-secret")
	}
}

// TestServerRegistersWithFederatedIdentity: with a federated client ID, the
// endpoint exchanges the host's ID token at the control server's token
// exchange (workload identity federation) and registers with the key minted
// with the resulting access token. No secret is involved.
func TestServerRegistersWithFederatedIdentity(t *testing.T) {
	const idToken = "header.payload.signature"

	var (
		mu       sync.Mutex
		exchange url.Values
		audience string
	)

	controlURL, keyReq := startControl(t, map[string]http.HandlerFunc{
		"POST /api/v2/oauth/token-exchange": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			exchange = r.PostForm
			mu.Unlock()
			writeAccessToken(w)
		},
	})

	ep := testEndpoint(t)

	srv, err := ep.server(Settings{
		ControlURL: controlURL,
		ClientID:   "fed123",
		IDToken: func(_ context.Context, aud string) (string, error) {
			mu.Lock()
			audience = aud
			mu.Unlock()
			return idToken, nil
		},
		StateDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if srv.AuthKey != "" {
		t.Errorf("AuthKey = %q, want none", srv.AuthKey)
	}

	registerAndCheckKey(t, ep, srv, keyReq)

	mu.Lock()
	defer mu.Unlock()

	u, _ := url.Parse(controlURL)
	if want := u.Host + "/fed123"; audience != want {
		t.Errorf("ID token audience = %q, want Headscale's default %q", audience, want)
	}
	if got := exchange.Get("client_id"); got != "fed123" {
		t.Errorf("exchanged client_id = %q, want %q", got, "fed123")
	}
	if got := exchange.Get("jwt"); got != idToken {
		t.Errorf("exchanged jwt = %q, want %q", got, idToken)
	}
}

func TestClientID(t *testing.T) {
	notEphemeral := false

	tests := []struct {
		name     string
		endpoint Endpoint
		clientID string
		want     string
	}{
		{"ephemeral preauthorized by default", Endpoint{}, "fed123", "fed123?ephemeral=true&preauthorized=true"},
		{"follows the endpoint", Endpoint{Ephemeral: &notEphemeral}, "fed123?ephemeral=true", "fed123?ephemeral=false&preauthorized=true"},
		{"keeps preauthorized", Endpoint{}, "fed123?preauthorized=false", "fed123?ephemeral=true&preauthorized=false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.endpoint.clientID(Settings{ClientID: tt.clientID})
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("clientID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSettingsValidate(t *testing.T) {
	src := func(context.Context, string) (string, error) { return "", nil }

	good := []Settings{
		{AuthKey: "tskey-auth-abc"},
		{ClientID: "fed123", ControlURL: "https://hs.example.com", IDToken: src},
		{ClientID: "fed123", Audience: "sts.example.com", IDToken: src},
	}
	for _, s := range good {
		if err := s.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v", s, err)
		}
	}

	bad := map[string]Settings{
		"both":            {ClientID: "fed123", AuthKey: "tskey-auth-abc", ControlURL: "https://hs.example.com", IDToken: src},
		"no audience":     {ClientID: "fed123", IDToken: src},
		"no token source": {ClientID: "fed123", ControlURL: "https://hs.example.com"},
	}
	for name, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("%s: Validate succeeded", name)
		}
	}

	aud, err := Settings{ClientID: "fed123?ephemeral=false", ControlURL: "https://hs.example.com/"}.audience()
	if err != nil || aud != "hs.example.com/fed123" {
		t.Errorf("audience() = %q, %v, want %q", aud, err, "hs.example.com/fed123")
	}
}

func jwtExpiring(exp time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, `{"exp":%d}`, exp.Unix()))
	return "e30." + payload + ".sig"
}

func TestCachedIDToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	var calls int
	var fail bool

	src := newCachedIDToken(func(_ context.Context, aud string) (string, error) {
		calls++
		if fail {
			fail = false
			return "", errors.New("no netmap")
		}
		return jwtExpiring(now.Add(5 * time.Minute)), nil
	}, func() time.Time { return now })

	ctx := context.Background()

	first, err := src(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := src(ctx, "a"); again != first || calls != 1 {
		t.Errorf("a valid token was not reused: calls=%d", calls)
	}

	if _, _ = src(ctx, "b"); calls != 2 {
		t.Errorf("another audience reused the token: calls=%d", calls)
	}

	// Close to expiry, a new token is fetched; a failure is retried.
	now = now.Add(4*time.Minute + 30*time.Second)
	fail = true

	if _, err := src(ctx, "a"); err != nil || calls != 4 {
		t.Errorf("src() = %v after %d calls, want a retried fetch (4 calls)", err, calls)
	}
}
