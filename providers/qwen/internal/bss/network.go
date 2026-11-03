package bss

import (
	"context"
	"errors"
	"net"
	"net/netip"
)

// Never include the hostname, URL or signed query in this error.
var errEndpointAddress = errors.New("bss: endpoint address is not allowed")

// IsGlobalUnicast describes address syntax, not public reachability. Admit
// ordinary public endpoints conservatively: exclude special-purpose IPv4
// assignments and IPv6 outside 2000::/3, plus special assignments within it.
// Protocol/translation/anycast special-use ranges are not BSS endpoints even
// where an individual assignment may be globally reachable.
var globalIPv6Space = netip.MustParsePrefix("2000::/3")

var specialEndpointRanges = [...]netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.31.196.0/24"),
	netip.MustParsePrefix("192.52.193.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.175.48.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2620:4f:8000::/48"),
	netip.MustParsePrefix("3ffe::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

func publicAddress(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	if addr.Is6() && !globalIPv6Space.Contains(addr) {
		return false
	}
	for _, prefix := range specialEndpointRanges {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// Resolve on every new connection, reject the whole answer set if any address
// is unsafe, then dial the checked numeric address (no second DNS resolution).
// Only explicitly opted-in HTTP loopback mocks may reach a non-public address.
func guardedDial(loopbackMock bool) func(context.Context, string, string) (net.Conn, error) {
	return guardedDialWith(loopbackMock, func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return net.DefaultResolver.LookupIPAddr(ctx, host)
	}, (&net.Dialer{}).DialContext)
}

// Keep lookup and numeric dialing injectable internally for socket-free policy
// tests. All production calls use the standard resolver and dialer above.
func guardedDialWith(loopbackMock bool,
	lookup func(context.Context, string) ([]net.IPAddr, error),
	dial func(context.Context, string, string) (net.Conn, error),
) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errEndpointAddress
		}
		addresses, err := lookup(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(addresses) == 0 {
			return nil, errEndpointAddress
		}
		for _, address := range addresses {
			if address.Zone != "" || loopbackMock && !address.IP.IsLoopback() || !loopbackMock && !publicAddress(address.IP) {
				return nil, errEndpointAddress
			}
		}
		var lastErr error
		for _, address := range addresses {
			conn, err := dial(ctx, network, net.JoinHostPort(address.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}
