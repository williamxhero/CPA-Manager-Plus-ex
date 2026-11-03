package bss

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientRejectsUntrustedTLSCertificate(t *testing.T) {
	var received atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { received.Add(1) }))
	defer server.Close()
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	client, err := NewClient(fixtureOptions(DefaultEndpoint))
	if err != nil {
		t.Fatal(err)
	}
	// Replace only the system socket boundary, leaving the actual BSS client's
	// TLS settings/verifier intact. No production insecure-TLS or transport
	// option is introduced, and the test never contacts a public endpoint.
	transport := client.http.Transport.(*http.Transport)
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	defer transport.CloseIdleConnections()
	_, err = client.Balance(context.Background())
	if err == nil || received.Load() != 0 || strings.Contains(err.Error(), "fixture") || strings.Contains(err.Error(), "https://") {
		t.Fatalf("untrusted TLS accepted or error exposed signed URL: requests=%d err=%v", received.Load(), err)
	}
}
