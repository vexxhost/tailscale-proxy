package endpoint

import (
	"fmt"
	"log"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/inetaf/tcpproxy"
	// Lets tsnet exchange an OAuth client secret passed as AuthKey for a
	// single-use auth key.
	_ "tailscale.com/feature/oauthkey"
	"tailscale.com/net/netutil"
	"tailscale.com/tsnet"
)

// authKey returns the key tsnet logs in with. An OAuth client secret is
// passed through for tsnet to exchange, with the attributes that tsnet reads
// from it filled in from the endpoint and settings.
func (ep *Endpoint) authKey(s Settings) (string, error) {
	key := s.AuthKey

	// Headscale issues OAuth client secrets as `hskey-client-` but tsnet
	// only exchanges secrets starting with `tskey-client-`, and Headscale
	// accepts both.
	if rest, ok := strings.CutPrefix(key, "hskey-client-"); ok {
		key = "tskey-client-" + rest
	}

	secret, query, _ := strings.Cut(key, "?")
	if !strings.HasPrefix(secret, "tskey-client-") {
		return key, nil
	}

	attrs, err := url.ParseQuery(query)
	if err != nil {
		return "", fmt.Errorf("invalid OAuth client secret attributes: %w", err)
	}

	attrs.Set("ephemeral", strconv.FormatBool(ep.ephemeral()))
	if attrs.Get("preauthorized") == "" {
		attrs.Set("preauthorized", "true")
	}
	if attrs.Get("baseURL") == "" && s.ControlURL != "" {
		attrs.Set("baseURL", strings.TrimRight(s.ControlURL, "/"))
	}

	return secret + "?" + attrs.Encode(), nil
}

func (ep *Endpoint) server(s Settings) (*tsnet.Server, error) {
	// NOTE(mnaser): AdvertiseTags requires that all strings are prefixed with `tag:`
	advertiseTags := make([]string, 0, len(ep.Tags))
	for _, tag := range ep.Tags {
		advertiseTags = append(advertiseTags, fmt.Sprintf("tag:%s", tag))
	}

	authKey, err := ep.authKey(s)
	if err != nil {
		return nil, err
	}

	return &tsnet.Server{
		Dir:           filepath.Join(s.StateDir, ep.Hostname),
		Hostname:      ep.Hostname,
		Ephemeral:     ep.ephemeral(),
		AdvertiseTags: advertiseTags,
		AuthKey:       authKey,
		ControlURL:    s.ControlURL,
	}, nil
}

func (ep *Endpoint) Start(s Settings) {
	srv, err := ep.server(s)
	if err != nil {
		log.Fatal(err)
	}

	srv.RegisterFallbackTCPHandler(func(src, dst netip.AddrPort) (handler func(net.Conn), intercept bool) {
		return func(conn net.Conn) {
			p := &tcpproxy.Proxy{
				ListenFunc: func(net, laddr string) (net.Listener, error) {
					return netutil.NewOneConnListener(conn, nil), nil
				},
			}
			p.AddRoute(conn.LocalAddr().String(), &tcpproxy.DialProxy{
				Addr: fmt.Sprintf("%s:%d", ep.IP, dst.Port()),
			})
			p.Start()
		}, true
	})

	for _, ln := range ep.Listeners {
		go ln.Start(ep.IP, srv)
	}

	err = srv.Start()
	if err != nil {
		log.Fatal(err)
	}
}

func (ep *Endpoint) Close() {
	for _, ln := range ep.Listeners {
		ln.Close()
	}
}
