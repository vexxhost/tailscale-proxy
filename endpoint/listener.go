package endpoint

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"

	"tailscale.com/tsnet"
	"tailscale.com/types/nettype"
)

func (l *Listener) startTLS(ip string, srv *tsnet.Server) {
	ln, err := srv.ListenTLS("tcp", fmt.Sprintf(":%d", l.Port))
	if err != nil {
		log.Fatal(err)
	}

	l.listener = ln

	rp := httputil.NewSingleHostReverseProxy(&url.URL{
		Scheme: "https",
		Host:   fmt.Sprintf("%s:%d", ip, l.Port),
	})
	rp.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{CipherSuites: []uint16{
			// TLS 1.0 - 1.2 cipher suites.
			tls.TLS_RSA_WITH_RC4_128_SHA,
			tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_RC4_128_SHA,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_RC4_128_SHA,
			tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			// TLS 1.3 cipher suites.
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
		}, InsecureSkipVerify: true},
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, err.Error(), http.StatusBadGateway)
	}

	log.Fatal(http.Serve(ln, rp))
}

func (l *Listener) startUDP(ip string, srv *tsnet.Server) {
	ln, err := srv.Listen("udp", fmt.Sprintf(":%d", l.Port))
	if err != nil {
		log.Fatal(err)
	}

	l.listener = ln

	for {
		conn, err := l.listener.Accept()
		if err != nil {
			log.Print(err)
			continue
		}

		go func(c net.Conn) {
			defer c.Close()
			packet := c.(nettype.ConnPacketConn)
			if err := forwardUDP(packet, fmt.Sprintf("%s:%d", ip, l.Port)); err != nil {
				log.Print(err)
			}
		}(conn)
	}
}

// forwardUDP proxies UDP traffic bidirectionally between a tailscale
// PacketConn and a remote target address.
func forwardUDP(packet nettype.ConnPacketConn, targetAddr string) error {
	remoteAddr, err := net.ResolveUDPAddr("udp", targetAddr)
	if err != nil {
		return err
	}

	remoteConn, err := net.DialUDP("udp", nil, remoteAddr)
	if err != nil {
		return err
	}
	defer remoteConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	// When one direction fails, close both connections to unblock the other.
	closeOnce := sync.OnceFunc(func() {
		packet.Close()
		remoteConn.Close()
	})

	// Copy from tailscale client to remote target
	go func() {
		defer wg.Done()
		defer closeOnce()
		buf := make([]byte, 65535)
		for {
			n, _, err := packet.ReadFrom(buf)
			if err != nil {
				return
			}
			if n == 0 {
				continue
			}
			if _, err := remoteConn.Write(buf[:n]); err != nil {
				return
			}
		}
	}()

	// Copy from remote target back to tailscale client
	go func() {
		defer wg.Done()
		defer closeOnce()
		buf := make([]byte, 65535)
		for {
			n, _, err := remoteConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if _, err := packet.WriteTo(buf[:n], packet.RemoteAddr()); err != nil {
				return
			}
		}
	}()

	wg.Wait()
	return nil
}

func (l *Listener) Start(ip string, srv *tsnet.Server) {
	switch l.Type {
	case ListenerTypeUDP:
		l.startUDP(ip, srv)
	case ListenerTypeTLS:
		l.startTLS(ip, srv)
	default:
		log.Fatalf("unsupported listener type %q", l.Type)
	}
}

func (l *Listener) Close() {
	l.listener.Close()
}
