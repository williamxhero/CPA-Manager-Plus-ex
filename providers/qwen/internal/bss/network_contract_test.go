package bss_test

import (
	"context"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"qwen-cliproxyapi/internal/bss"
)

// Only synthetic literals are used; no environment/profile/credential files.
func optionsFor(endpoint string) bss.Options {
	return bss.Options{Endpoint: endpoint, AccessKeyID: "fixture-id* /中", AccessKeySecret: "fixture-secret* /中", AllowHTTP: true}
}

func TestEndpointRejectsPrivateAndSpecialHTTPS(t *testing.T) {
	for _, host := range []string{
		"10.0.0.1", "172.16.0.1", "192.168.1.1", "127.0.0.1", "localhost", "LOCALHOST.",
		"0.0.0.0", "169.254.169.254", "100.100.100.200", "224.0.0.1", "255.255.255.255",
		"[::]", "[::1]", "[fc00::1]", "[fe80::1]", "[ff02::1]", "[::ffff:127.0.0.1]", "[fe80::1%25fixture]",
	} {
		t.Run(host, func(t *testing.T) {
			if _, err := bss.NewClient(optionsFor("https://" + host)); err == nil {
				t.Fatal("accepted non-public HTTPS endpoint; AllowHTTP must not authorize private HTTPS")
			}
		})
	}
}

func TestHTTPMockAllowanceNeverAuthorizesPrivateNetworks(t *testing.T) {
	for _, host := range []string{"10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "[fc00::1]", "127.0.0.1.fixture.test"} {
		t.Run(host, func(t *testing.T) {
			if _, err := bss.NewClient(optionsFor("http://" + host)); err == nil {
				t.Fatal("HTTP mock allowance escaped loopback")
			}
		})
	}
}

// The real resolver talks to a local synthetic DNS boundary. Replacing the
// default resolver is restored at cleanup; these tests intentionally are serial.
func privateDNS(t *testing.T, addresses ...net.IP) {
	t.Helper()
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, peer, err := server.ReadFrom(buf)
			if err != nil {
				return
			}
			if n < 17 {
				continue
			}
			query := append([]byte(nil), buf[:n]...)
			end := 12
			for end < n && query[end] != 0 {
				end += int(query[end]) + 1
			}
			end += 5 // terminating zero, QTYPE, QCLASS
			if end > n {
				continue
			}
			kind := binary.BigEndian.Uint16(query[end-4 : end-2])
			reply := append([]byte(nil), query[:end]...)
			binary.BigEndian.PutUint16(reply[2:4], 0x8180)
			binary.BigEndian.PutUint16(reply[6:8], 0)
			binary.BigEndian.PutUint16(reply[8:10], 0)
			binary.BigEndian.PutUint16(reply[10:12], 0)
			var count uint16
			for _, ip := range addresses {
				if kind == 1 && ip.To4() != nil {
					reply = append(reply, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 0, 0, 4)
					reply = append(reply, ip.To4()...)
					count++
				} else if kind == 28 && ip.To4() == nil && ip.To16() != nil {
					reply = append(reply, 0xc0, 0x0c, 0, 28, 0, 1, 0, 0, 0, 0, 0, 16)
					reply = append(reply, ip.To16()...)
					count++
				}
			}
			binary.BigEndian.PutUint16(reply[6:8], count)
			_, _ = server.WriteTo(reply, peer)
		}
	}()
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", server.LocalAddr().String())
	}}
	t.Cleanup(func() {
		net.DefaultResolver = previous
		_ = server.Close()
		<-done
	})
}

func TestClientRejectsMixedAndIPv6PrivateDNS(t *testing.T) {
	for _, tc := range []struct {
		name      string
		addresses []net.IP
	}{
		{"mixed-v4", []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("127.0.0.1")}},
		{"private-v6", []net.IP{net.ParseIP("fc00::1")}},
		{"mixed-v4-v6", []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("::1")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			privateDNS(t, tc.addresses...)
			client, err := bss.NewClient(optionsFor("https://bss-fixture.test"))
			if err != nil {
				t.Fatal(err)
			}
			// Repeat on one client: each attempt must validate DNS, not fall
			// through to any public peer or cache an unchecked address.
			for i := 0; i < 2; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_, err := client.Balance(ctx)
				cancel()
				if err == nil || !strings.Contains(err.Error(), "endpoint address is not allowed") {
					t.Fatalf("unsafe DNS set escaped guard: %v", err)
				}
			}
		})
	}
}

func TestClientEnvironmentProxyCannotBypassPrivateDNSGuard(t *testing.T) {
	var proxyCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyCalls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("ALL_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	privateDNS(t, net.ParseIP("127.0.0.1"))
	client, err := bss.NewClient(optionsFor("https://bss-fixture.test"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := client.Balance(ctx); err == nil || !strings.Contains(err.Error(), "endpoint address is not allowed") {
		t.Fatalf("proxy bypassed address checks: %v", err)
	}
	if proxyCalls.Load() != 0 {
		t.Fatal("signed credentials exposed to environment proxy")
	}
}

func TestClientRejectsPrivateDNSBeforeConnecting(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	privateDNS(t, net.ParseIP("127.0.0.1"))
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	client, err := bss.NewClient(optionsFor("https://bss-fixture.test:" + port))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = client.Balance(ctx)
	if err == nil || !strings.Contains(err.Error(), "endpoint address is not allowed") {
		t.Fatalf("private DNS did not fail at the address guard: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatal("signed request reached private DNS destination")
	}
}
