package endpoint

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"strings"

	"github.com/vexxhost/tailscale-proxy/internal/tsnet"
	"golang.org/x/oauth2/clientcredentials"
	"inet.af/tcpproxy"
	"tailscale.com/client/tailscale"
	"tailscale.com/net/netutil"
)

func (ep *Endpoint) Start() {
	// NOTE(mnaser): AdvertiseTags requires that all strings are prefixed with `tag:`
	advertiseTags := make([]string, 0, len(ep.Tags))
	for _, tag := range ep.Tags {
		advertiseTags = append(advertiseTags, fmt.Sprintf("tag:%s", tag))
	}

	authkey := os.Getenv("TS_AUTHKEY")
	if strings.HasPrefix(authkey, "tskey-client-") {
		tailscale.I_Acknowledge_This_API_Is_Unstable = true

		baseURL := "https://api.tailscale.com"

		credentials := clientcredentials.Config{
			ClientID:     "some-client-id", // ignored
			ClientSecret: authkey,
			TokenURL:     baseURL + "/api/v2/oauth/token",
		}

		tsClient := tailscale.NewClient("-", nil)
		tsClient.UserAgent = "tailscale-cli"
		tsClient.HTTPClient = credentials.Client(context.TODO())
		tsClient.BaseURL = baseURL

		caps := tailscale.KeyCapabilities{
			Devices: tailscale.KeyDeviceCapabilities{
				Create: tailscale.KeyDeviceCreateCapabilities{
					Reusable:      false,
					Ephemeral:     true,
					Preauthorized: true,
					Tags:          advertiseTags,
				},
			},
		}

		var err error
		authkey, _, err = tsClient.CreateKey(context.TODO(), caps)
		if err != nil {
			log.Fatal(err)
		}
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

	go func() {
		srv.Start()
		defer srv.Close()
	}()
}

func (ep *Endpoint) Close() {
	for _, ln := range ep.Listeners {
		ln.Close()
	}
}
