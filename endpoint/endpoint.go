package endpoint

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"

	"github.com/inetaf/tcpproxy"
	"tailscale.com/client/tailscale/v2"
	"tailscale.com/net/netutil"
	"tailscale.com/tsnet"
)

func (ep *Endpoint) Start() {
	// NOTE(mnaser): AdvertiseTags requires that all strings are prefixed with `tag:`
	advertiseTags := make([]string, 0, len(ep.Tags))
	for _, tag := range ep.Tags {
		advertiseTags = append(advertiseTags, fmt.Sprintf("tag:%s", tag))
	}

	authkey := os.Getenv("TS_AUTHKEY")
	if os.Getenv("TS_OAUTH_CLIENT_ID") != "" {
		client := &tailscale.Client{
			Tailnet: os.Getenv("TS_TAILNET"),
			Auth: &tailscale.OAuth{
				ClientID:     os.Getenv("TS_OAUTH_CLIENT_ID"),
				ClientSecret: os.Getenv("TS_OAUTH_CLIENT_SECRET"),
				Scopes:       []string{"auth_keys"},
			},
		}

		req := tailscale.CreateKeyRequest{}
		req.Capabilities.Devices.Create.Reusable = false
		req.Capabilities.Devices.Create.Ephemeral = true
		req.Capabilities.Devices.Create.Preauthorized = true
		req.Capabilities.Devices.Create.Tags = advertiseTags

		key, err := client.Keys().CreateAuthKey(context.TODO(), req)
		if err != nil {
			log.Fatal(err)
		}

		authkey = key.Key
	}

	srv := &tsnet.Server{
		Dir:           fmt.Sprintf("/var/lib/tailscale-proxy/%s", ep.Hostname),
		Hostname:      ep.Hostname,
		Ephemeral:     true,
		AdvertiseTags: advertiseTags,
		AuthKey:       authkey,
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

	err := srv.Start()
	if err != nil {
		log.Fatal(err)
	}
}

func (ep *Endpoint) Close() {
	for _, ln := range ep.Listeners {
		ln.Close()
	}
}
