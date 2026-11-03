//go:build cpa_entry_fixture

package app

// Only DNS, numeric socket dialing and trust roots vary in this explicit
// acceptance build. The real HTTPS safety transport and parser still execute.
import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"os"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
)

func providerRuntimeNetwork() balance.NetworkOptions {
	address, path := os.Getenv("CPAMP_PROVIDER_FIXTURE_ADDRESS"), os.Getenv("CPAMP_PROVIDER_FIXTURE_CA")
	if address == "" && path == "" {
		return balance.NetworkOptions{}
	}
	host, _, err := net.SplitHostPort(address)
	cert, readErr := os.ReadFile(path)
	roots := x509.NewCertPool()
	if err != nil || host != "127.0.0.1" || readErr != nil || !roots.AppendCertsFromPEM(cert) {
		panic("provider fixture requires explicit loopback TLS dependency")
	}
	return balance.NetworkOptions{RootCAs: roots,
		LookupIP: func(_ context.Context, host string) ([]net.IPAddr, error) {
			if host != "api.deepseek.com" {
				return nil, errors.New("fixture DNS refused")
			}
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		},
		DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
			if target != "127.0.0.1:443" {
				return nil, errors.New("fixture socket refused")
			}
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
	}
}
