package balance

import (
	"errors"
	"net/netip"
	"net/url"
	"strings"
)

// Private targets are exact administrator-approved IPs, not wildcard networks.
// Link-local, metadata, multicast and unspecified addresses are never permitted.
func ValidateSource(endpoint string, permits []string) error {
	return ValidateAdapterSource("deepseek", endpoint, permits)
}

// Each protocol owns an exact path. Preserve ValidateSource as the DeepSeek-only
// compatibility entry point; adding an adapter never widens its route policy.
func ValidateAdapterSource(adapter, endpoint string, permits []string) error {
	if adapter == AliyunPrepaidCashV20171214 {
		if endpoint != AliyunBalanceEndpoint || len(permits) != 0 {
			return errors.New("fixed China-site official billing origin required")
		}
		return nil
	}
	if adapter == VolcengineAccountBalanceV20220101 {
		if endpoint != VolcengineBalanceEndpoint || len(permits) != 0 {
			return errors.New("fixed official billing origin required")
		}
		return nil
	}
	path := ""
	switch adapter {
	case "deepseek":
		path = "/user/balance"
	case "deepinfra":
		path = "/payment/checklist"
	case NewAPIUserSelfV1:
		path = "/api/user/self"
	case NewAPITokenUsageV1:
		path = "/api/usage/token/"
	default:
		return errors.New("invalid adapter")
	}
	u, e := url.Parse(endpoint)
	if e != nil || len(endpoint) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != path || (adapter != "deepseek" && (u.Opaque != "" || u.RawPath != "" || u.ForceQuery || strings.Contains(endpoint, "#"))) || strings.Contains(u.Hostname(), "%") {
		return errors.New("invalid source")
	}
	for _, p := range permits {
		ip, e := netip.ParseAddr(p)
		if e != nil || !ip.IsValid() || forbiddenIP(ip) {
			return errors.New("invalid permit")
		}
	}
	return nil
}

// Conservative egress classes, not a routability heuristic. In particular
// IsGlobalUnicast includes deprecated/site-local and IPv4 transition addresses.
// RFC6052/RFC8215 translation and 6to4/Teredo are unsupported, even with a
// private permit. IPv4-mapped literals are normalized before all policy checks.
var forbiddenV4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
}
var ordinaryV6 = netip.MustParsePrefix("2000::/3")
var unsupportedV6 = []netip.Prefix{
	netip.MustParsePrefix("2001::/23"),     // IETF specials, including Teredo
	netip.MustParsePrefix("2001:db8::/32"), // documentation
	netip.MustParsePrefix("2002::/16"),     // 6to4 embeds an IPv4 destination
	netip.MustParsePrefix("3fff::/20"),     // documentation
}

func forbiddenIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || (!ip.IsGlobalUnicast() && !ip.IsLoopback()) || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return true
	}
	if ip.String() == "168.63.129.16" || ip.String() == "fd00:ec2::254" {
		return true
	}
	if ip.Is4() {
		for _, prefix := range forbiddenV4 {
			if prefix.Contains(ip) {
				return true
			}
		}
		return false
	}
	// Permit classification of native ULA/loopback ONLY; the dial policy still
	// requires an exact administrator permit for these addresses. Everything
	// outside native GUA/ULA/loopback (NAT64, site-local, compatible, etc.) fails closed.
	if !ordinaryV6.Contains(ip) && !ip.IsPrivate() && !ip.IsLoopback() {
		return true
	}
	for _, prefix := range unsupportedV6 {
		if prefix.Contains(ip) {
			return true
		}
	}
	bytes := ip.As16()
	// ISATAP interface identifiers can tunnel the low 32 IPv4 bits under GUA.
	if (bytes[8] == 0 || bytes[8] == 2) && bytes[9] == 0 && bytes[10] == 0x5e && bytes[11] == 0xfe {
		return true
	}
	return false
}
