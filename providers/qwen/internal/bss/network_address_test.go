package bss

import (
	"context"
	"errors"
	"net"
	"testing"
)

// Admission only: neither classification nor NewClient resolves or connects.
// Include both ends of special-use ranges and IPv4-mapped forms independently
// of the production prefix table. AllowHTTP must never weaken HTTPS admission.
func TestGlobalRoutableRejectsSpecialPurposeHTTPS(t *testing.T) {
	for _, literal := range []string{
		// The independent review's four original failures.
		"0.0.0.1", "198.18.0.1", "240.0.0.1", "100::1",
		// IPv4 special-use, protocol-assignment and documentation ranges.
		"0.255.255.255", "10.0.0.1", "10.255.255.255",
		"100.64.0.0", "100.127.255.255", "127.0.0.1",
		"169.254.0.0", "169.254.255.255", "172.16.0.0", "172.31.255.255",
		"192.0.0.0", "192.0.0.255", "192.0.2.0", "192.0.2.255",
		"192.31.196.1", "192.52.193.1", "192.88.99.0", "192.88.99.255",
		"192.168.0.0", "192.168.255.255", "192.175.48.1",
		"198.18.0.0", "198.19.255.255", "198.51.100.0", "198.51.100.255",
		"203.0.113.0", "203.0.113.255", "224.0.0.0", "239.255.255.255",
		"240.0.0.0", "255.255.255.254", "255.255.255.255",
		// Non-global IPv6, translation, discard, local and multicast space.
		"::", "::1", "::192.0.2.1", "64:ff9b::808:808", "64:ff9b:1::1",
		"100::", "100::ffff:ffff:ffff:ffff", "100:0:0:1::1",
		"4000::1", "5f00::1", "fc00::1", "fdff:ffff::1", "fe80::1", "ff02::1",
		// Special assignments *inside* 2000::/3 are not ordinary endpoints.
		"2001::1", "2001:1::1", "2001:2::1", "2001:3::1", "2001:4:112::1",
		"2001:10::1", "2001:20::1", "2001:30::1", "2001:1ff:ffff::1",
		"2001:db8::", "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff",
		"2002::1", "2002:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "2620:4f:8000::1",
		"3ffe::1", "3ffe:ffff::1", "3fff::", "3fff:fff:ffff:ffff:ffff:ffff:ffff:ffff",
		// Unmapping must not smuggle special IPv4 past the IPv6 policy.
		"::ffff:0.0.0.1", "::ffff:198.18.0.1", "::ffff:240.0.0.1", "::ffff:192.0.2.1",
	} {
		t.Run(literal, func(t *testing.T) {
			ip := net.ParseIP(literal)
			if ip == nil {
				t.Fatal("invalid test literal")
			}
			if publicAddress(ip) {
				t.Error("special-use address admitted as a public destination")
			}
			for _, allowHTTP := range []bool{false, true} {
				_, err := NewClient(Options{Endpoint: "https://" + net.JoinHostPort(literal, "443"), AllowHTTP: allowHTTP})
				if err == nil {
					t.Errorf("HTTPS special-use endpoint accepted (AllowHTTP=%t)", allowHTTP)
				}
			}
		})
	}
}

func TestGlobalRoutableAcceptsOrdinaryPublicHTTPS(t *testing.T) {
	for _, literal := range []string{
		"8.8.8.8", "1.1.1.1", "223.5.5.5",
		"2001:4860:4860::8888", "2606:4700:4700::1111", "2400:3200::1", "::ffff:8.8.8.8",
	} {
		t.Run(literal, func(t *testing.T) {
			if !publicAddress(net.ParseIP(literal)) {
				t.Fatal("ordinary public address rejected")
			}
			for _, allowHTTP := range []bool{false, true} {
				if _, err := NewClient(Options{Endpoint: "https://" + net.JoinHostPort(literal, "443"), AllowHTTP: allowHTTP}); err != nil {
					t.Fatalf("ordinary HTTPS endpoint rejected: %v", err)
				}
			}
		})
	}
}

func TestGlobalRoutableRejectsInvalidIP(t *testing.T) {
	for _, ip := range []net.IP{nil, {}, {1, 2, 3}, {1, 2, 3, 4, 5}} {
		if publicAddress(ip) {
			t.Fatal("invalid IP admitted")
		}
	}
}

func TestGlobalRoutableHTTPStillRequiresExplicitLoopbackMode(t *testing.T) {
	for _, tc := range []struct {
		endpoint  string
		allow, ok bool
	}{
		{"http://127.0.0.1", false, false},
		{"http://[::1]", false, false},
		{"http://127.0.0.1", true, true},
		{"http://[::1]", true, true},
		{"http://[::ffff:127.0.0.1]", true, true},
		{"http://localhost", true, true},
		{"http://8.8.8.8", true, false},
		{"http://10.0.0.1", true, false},
		{"https://127.0.0.1", true, false},
		{"https://[::1]", true, false},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			_, err := NewClient(Options{Endpoint: tc.endpoint, AllowHTTP: tc.allow})
			if (err == nil) != tc.ok {
				t.Fatalf("test-only loopback policy changed: allow=%t err=%v", tc.allow, err)
			}
		})
	}
}

// These collaborators do not use the OS resolver, sockets or listeners. Even
// when admission regresses, the fake dial cannot reach an unsafe destination.
func TestGlobalRoutableDNSMixedAnswersFailClosed(t *testing.T) {
	unsafe := []net.IPAddr{{}, {IP: net.IP{1, 2, 3}}, {IP: net.ParseIP("8.8.8.8"), Zone: "fixture"}}
	for _, literal := range []string{
		"0.0.0.1", "198.18.0.1", "240.0.0.1", "100::1",
		"10.0.0.1", "127.0.0.1", "100.64.0.1", "169.254.169.254", "192.0.2.1",
		"::ffff:198.18.0.1", "fc00::1", "fe80::1", "64:ff9b::808:808",
		"2001:db8::1", "2002::1", "3fff::1", "ff02::1",
	} {
		unsafe = append(unsafe, net.IPAddr{IP: net.ParseIP(literal)})
	}
	for _, bad := range unsafe {
		for _, good := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
			for _, badFirst := range []bool{false, true} {
				name := bad.String() + "/public=" + good + "/bad-last"
				answers := []net.IPAddr{{IP: net.ParseIP(good)}, bad}
				if badFirst {
					name = bad.String() + "/public=" + good + "/bad-first"
					answers[0], answers[1] = answers[1], answers[0]
				}
				t.Run(name, func(t *testing.T) {
					lookups, dials := 0, 0
					ctx := context.Background()
					guard := guardedDialWith(false, func(gotCtx context.Context, host string) ([]net.IPAddr, error) {
						lookups++
						if gotCtx != ctx || host != "fixture.invalid" {
							t.Fatal("lookup context/host changed")
						}
						return answers, nil
					}, func(context.Context, string, string) (net.Conn, error) {
						dials++
						return nil, errors.New("unexpected in-memory dial")
					})
					conn, err := guard(ctx, "tcp", "fixture.invalid:443")
					if conn != nil || !errors.Is(err, errEndpointAddress) || lookups != 1 || dials != 0 {
						t.Fatalf("mixed set did not fail before dial: conn=%v err=%v lookups=%d dials=%d", conn, err, lookups, dials)
					}
				})
			}
		}
	}
}

func TestGlobalRoutableDNSNumericFallback(t *testing.T) {
	for _, succeed := range []bool{false, true} {
		name := "all-dials-fail"
		if succeed {
			name = "second-dial-succeeds"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			lookups, dials := 0, 0
			dialErr := errors.New("synthetic numeric dial failure")
			want := []string{"8.8.8.8:443", "[2606:4700:4700::1111]:443"}
			guard := guardedDialWith(false, func(context.Context, string) ([]net.IPAddr, error) {
				lookups++
				return []net.IPAddr{{IP: net.ParseIP("::ffff:8.8.8.8")}, {IP: net.ParseIP("2606:4700:4700::1111")}}, nil
			}, func(gotCtx context.Context, network, address string) (net.Conn, error) {
				dials++
				if gotCtx != ctx || network != "tcp" || dials > len(want) || address != want[dials-1] {
					t.Fatalf("dial was not the checked numeric candidate: %q %q", network, address)
				}
				if succeed && dials == 2 {
					conn, peer := net.Pipe()
					t.Cleanup(func() { _ = conn.Close(); _ = peer.Close() })
					return conn, nil
				}
				return nil, dialErr
			})
			conn, err := guard(ctx, "tcp", "fixture.invalid:443")
			if lookups != 1 || dials != 2 {
				t.Fatalf("numeric fallback must not resolve again: lookups=%d dials=%d", lookups, dials)
			}
			if succeed {
				if err != nil || conn == nil {
					t.Fatalf("second safe candidate not returned: %v", err)
				}
			} else if conn != nil || !errors.Is(err, dialErr) {
				t.Fatalf("last dial error not preserved: %v", err)
			}
		})
	}
}

func TestGlobalRoutableDNSRechecksEachConnection(t *testing.T) {
	lookups, dials := 0, 0
	dialErr := errors.New("synthetic dial failure")
	guard := guardedDialWith(false, func(context.Context, string) ([]net.IPAddr, error) {
		lookups++
		answers := []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}
		if lookups > 1 {
			answers = append(answers, net.IPAddr{IP: net.ParseIP("198.18.0.1")})
		}
		return answers, nil
	}, func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, dialErr
	})
	if _, err := guard(context.Background(), "tcp", "fixture.invalid:443"); !errors.Is(err, dialErr) {
		t.Fatalf("first public answer not dialed: %v", err)
	}
	if _, err := guard(context.Background(), "tcp", "fixture.invalid:443"); !errors.Is(err, errEndpointAddress) {
		t.Fatalf("later unsafe answer escaped fresh admission: %v", err)
	}
	if lookups != 2 || dials != 1 {
		t.Fatalf("fresh connection reused an unchecked answer: lookups=%d dials=%d", lookups, dials)
	}
}

func TestGlobalRoutableDNSLoopbackMode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		literals []string
		allowed  bool
	}{
		{"all-loopback", []string{"127.0.0.1", "::1", "::ffff:127.0.0.2"}, true},
		{"public-first", []string{"8.8.8.8", "127.0.0.1"}, false},
		{"public-last", []string{"::1", "2606:4700:4700::1111"}, false},
		{"private", []string{"10.0.0.1"}, false},
		{"special-v4", []string{"198.18.0.1"}, false},
		{"special-v6", []string{"100::1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var answers []net.IPAddr
			for _, literal := range tc.literals {
				answers = append(answers, net.IPAddr{IP: net.ParseIP(literal)})
			}
			dials := 0
			dialErr := errors.New("synthetic loopback dial failure")
			guard := guardedDialWith(true, func(context.Context, string) ([]net.IPAddr, error) {
				return answers, nil
			}, func(context.Context, string, string) (net.Conn, error) {
				dials++
				return nil, dialErr
			})
			_, err := guard(context.Background(), "tcp", "localhost:80")
			if tc.allowed {
				if !errors.Is(err, dialErr) || dials != len(answers) {
					t.Fatalf("explicit loopback fixtures no longer admitted: %v, dials=%d", err, dials)
				}
			} else if !errors.Is(err, errEndpointAddress) || dials != 0 {
				t.Fatalf("loopback mode permitted a non-loopback answer: %v, dials=%d", err, dials)
			}
		})
	}
}

func TestGlobalRoutableDNSLookupFailure(t *testing.T) {
	lookupErr := errors.New("synthetic resolver failure")
	for _, tc := range []struct {
		name, address string
		lookupErr     error
		wantErr       error
		wantLookups   int
	}{
		{"empty-answer", "fixture.invalid:443", nil, errEndpointAddress, 1},
		{"lookup-error", "fixture.invalid:443", lookupErr, lookupErr, 1},
		{"malformed-address", "fixture.invalid", nil, errEndpointAddress, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookups, dials := 0, 0
			guard := guardedDialWith(false, func(context.Context, string) ([]net.IPAddr, error) {
				lookups++
				return nil, tc.lookupErr
			}, func(context.Context, string, string) (net.Conn, error) {
				dials++
				return nil, errors.New("unexpected in-memory dial")
			})
			conn, err := guard(context.Background(), "tcp", tc.address)
			if conn != nil || !errors.Is(err, tc.wantErr) || lookups != tc.wantLookups || dials != 0 {
				t.Fatalf("failed lookup attempted a connection: conn=%v err=%v lookups=%d dials=%d", conn, err, lookups, dials)
			}
		})
	}
}
