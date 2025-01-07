package endpoint

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/vexxhost/tailscale-proxy/internal/tsnet"
	"inet.af/tcpproxy"
	"tailscale.com/net/netutil"
)

func (ep *Endpoint) Start() {
	// NOTE(mnaser): AdvertiseTags requires that all strings are prefixed with `tag:`
	advertiseTags := make([]string, 0, len(ep.Tags))
	for _, tag := range ep.Tags {
		advertiseTags = append(advertiseTags, fmt.Sprintf("tag:%s", tag))
	}

	srv := &tsnet.Server{
		Dir:           fmt.Sprintf("/var/lib/tailscale-proxy/%s", ep.Hostname),
		Hostname:      ep.Hostname,
		Ephemeral:     true,
		AdvertiseTags: advertiseTags,
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
		ln.Start(ep.IP, srv)
	}
}

func (ep *Endpoint) Close() {
	for _, ln := range ep.Listeners {
		ln.Close()
	}
}
