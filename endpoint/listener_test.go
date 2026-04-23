package endpoint

import (
	"net"
	"testing"
	"time"
)

// connPacketConn wraps a net.UDPConn to satisfy nettype.ConnPacketConn
// by providing both net.Conn and net.PacketConn interfaces with a fixed
// remote address for WriteTo responses.
type connPacketConn struct {
	*net.UDPConn
	remote net.Addr
}

func (c *connPacketConn) RemoteAddr() net.Addr {
	return c.remote
}

func TestForwardUDP(t *testing.T) {
	// Start a UDP echo server (the "target")
	target, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := target.ReadFrom(buf)
			if err != nil {
				return
			}
			target.WriteTo(buf[:n], addr)
		}
	}()

	// Create a UDP socket pair for the "tailscale peer" side.
	// peerConn is what the forwarder reads/writes (simulates tsnet's ConnPacketConn).
	// clientConn is what the test sends/receives on (simulates the tailscale peer).
	peerListener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer peerListener.Close()

	clientConn, err := net.DialUDP("udp", nil, peerListener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	// Send a discovery packet so peerListener knows the client's address
	_, err = clientConn.Write([]byte("init"))
	if err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 65535)
	n, clientAddr, err := peerListener.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "init" {
		t.Fatalf("expected init, got %q", string(buf[:n]))
	}

	// Wrap peerListener as a ConnPacketConn
	peerUDP := peerListener.(*net.UDPConn)
	cpc := &connPacketConn{UDPConn: peerUDP, remote: clientAddr}

	// Run the forwarder in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- forwardUDP(cpc, target.LocalAddr().String())
	}()

	// Test: send data from client → should be echoed back via forwarder → target → forwarder → client
	testMessages := []string{"hello", "world", "udp forwarding works!"}
	for _, msg := range testMessages {
		_, err = clientConn.Write([]byte(msg))
		if err != nil {
			t.Fatalf("write %q: %v", msg, err)
		}

		clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err = clientConn.Read(buf)
		if err != nil {
			t.Fatalf("read echo for %q: %v", msg, err)
		}

		if got := string(buf[:n]); got != msg {
			t.Errorf("expected %q, got %q", msg, got)
		}
	}

	// Clean shutdown: close the peer conn to simulate session end.
	// UDP is connectionless so closing clientConn doesn't signal peerUDP.
	// In production, tsnet closes the conn when the session expires.
	peerUDP.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Logf("forwardUDP returned (expected after close): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("forwardUDP did not return after peer close")
	}
}
