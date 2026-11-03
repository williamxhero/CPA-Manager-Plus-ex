package bss

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	DefaultEndpoint  = "https://business.aliyuncs.com/"
	DefaultTimeout   = 20 * time.Second
	MaxTimeout       = 2 * time.Minute
	MaxResponseBytes = 1 << 20
)

type Options struct {
	AccessKeyID     string
	AccessKeySecret string
	Region          string
	Endpoint        string
	AllowHTTP       bool
	Timeout         time.Duration
}

type Client struct {
	options  Options
	endpoint *url.URL
	http     *http.Client
}

func NewClient(options Options) (*Client, error) {
	if options.Endpoint == "" {
		options.Endpoint = DefaultEndpoint
	}
	u, err := url.Parse(options.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(options.Endpoint, "#") || u.Opaque != "" {
		return nil, errors.New("bss: invalid endpoint")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		loopback := strings.EqualFold(u.Hostname(), "localhost") || ip != nil && ip.IsLoopback()
		if u.Scheme != "http" || !options.AllowHTTP || !loopback {
			return nil, errors.New("bss: endpoint requires HTTPS; HTTP is allowed only for explicitly enabled loopback mocks")
		}
	}
	if u.Scheme == "https" {
		host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.Contains(host, "%") {
			return nil, errEndpointAddress
		}
		if ip := net.ParseIP(host); ip != nil && !publicAddress(ip) {
			return nil, errEndpointAddress
		}
	}
	if u.Path == "" {
		u.Path = "/"
	}
	if options.Timeout <= 0 {
		options.Timeout = DefaultTimeout
	} else if options.Timeout < time.Second {
		options.Timeout = time.Second
	} else if options.Timeout > MaxTimeout {
		options.Timeout = MaxTimeout
	}
	return &Client{options: options, endpoint: u, http: &http.Client{
		Timeout: options.Timeout,
		// No environment proxy: a proxy could bypass destination DNS/IP checks.
		// TLS verification remains enabled with the standard system trust store.
		Transport: &http.Transport{
			DialContext:         guardedDial(u.Scheme == "http"),
			TLSHandshakeTimeout: 10 * time.Second,
			IdleConnTimeout:     90 * time.Second,
			MaxIdleConns:        10,
			ForceAttemptHTTP2:   true,
		},
		// Redirects could forward the signed access key query to another host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) Balance(ctx context.Context) (BalanceResult, error) {
	body, err := c.fetch(ctx, "QueryAccountBalance", nil)
	if err != nil {
		return BalanceResult{}, err
	}
	result, err := ParseBalance(body)
	return result, c.sanitizeError(err)
}

func (c *Client) Bill(ctx context.Context, billingCycle string) (BillResult, error) {
	if _, err := time.Parse("2006-01", billingCycle); err != nil {
		return BillResult{}, errors.New("bss: billing cycle must be YYYY-MM")
	}
	body, err := c.fetch(ctx, "QueryAccountBill", map[string]string{
		"BillingCycle": billingCycle, "Granularity": "MONTHLY", "IsGroupByProduct": "true",
	})
	if err != nil {
		return BillResult{}, err
	}
	result, err := ParseBill(body)
	return result, c.sanitizeError(err)
}

func (c *Client) Packages(ctx context.Context) ([]ResourcePackage, error) {
	body, err := c.fetch(ctx, "QueryResourcePackageInstances", nil)
	if err != nil {
		return nil, err
	}
	result, err := ParsePackages(body)
	return result, c.sanitizeError(err)
}

func (c *Client) fetch(ctx context.Context, action string, extra map[string]string) ([]byte, error) {
	if strings.TrimSpace(c.options.AccessKeyID) == "" || strings.TrimSpace(c.options.AccessKeySecret) == "" {
		return nil, errors.New("bss: access key ID and secret are required")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, errors.New("bss: could not generate signature nonce")
	}
	// RFC4122 version 4, represented without dashes as accepted by RPC.
	nonce[6] = nonce[6]&0x0f | 0x40
	nonce[8] = nonce[8]&0x3f | 0x80
	params := map[string]string{
		"Format": "JSON", "Version": "2017-12-14", "AccessKeyId": c.options.AccessKeyID,
		"SignatureMethod": "HMAC-SHA1", "SignatureVersion": "1.0",
		"SignatureNonce": hex.EncodeToString(nonce[:]),
		"Timestamp":      time.Now().UTC().Format("2006-01-02T15:04:05Z"), "Action": action,
	}
	if c.options.Region != "" {
		params["RegionId"] = c.options.Region
	}
	for key, value := range extra {
		params[key] = value
	}
	u := *c.endpoint
	u.RawQuery = CanonicalQuery(params) + "&Signature=" + PercentEncode(Sign(params, c.options.AccessKeySecret))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("bss: could not construct request")
	}
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("bss: request canceled: %w", ctx.Err())
		}
		if errors.Is(err, errEndpointAddress) {
			return nil, errEndpointAddress
		}
		// net/http errors contain the URL, including the signed access key query.
		return nil, errors.New("bss: HTTP request failed (network error or timeout)")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, errors.New("bss: could not read response")
	}
	if len(body) > MaxResponseBytes {
		return nil, errors.New("bss: response exceeds size limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, apiErr := decodeEnvelope(body)
		if apiErr != nil {
			return nil, fmt.Errorf("bss: HTTP %d: %s", response.StatusCode, c.sanitizeError(apiErr))
		}
		return nil, fmt.Errorf("bss: HTTP %d", response.StatusCode)
	}
	return body, nil
}

func (c *Client) sanitizeError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(safeText(err.Error(), c.options.AccessKeyID, c.options.AccessKeySecret))
}

var safeCode = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

func safeText(text string, secrets ...string) string {
	lower := strings.ToLower(text)
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		encoded, form := PercentEncode(secret), url.QueryEscape(secret)
		for _, variant := range []string{secret, encoded, form, PercentEncode(encoded), PercentEncode(form), url.QueryEscape(encoded), url.QueryEscape(form)} {
			// Percent escapes are case-insensitive. Case-folding the whole
			// candidate deliberately prefers over-redaction over a secret leak.
			if strings.Contains(lower, strings.ToLower(variant)) {
				return "bss: upstream error (sensitive details redacted)"
			}
		}
	}
	for _, marker := range []string{"accesskeyid=", "accesskeysecret", "access_key", "signature=", "signature%", "http://", "https://", "%3d", "%253d"} {
		if strings.Contains(lower, marker) {
			return "bss: upstream error (sensitive details redacted)"
		}
	}
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	if len(text) > 256 {
		return "bss: upstream error (message too long)"
	}
	return text
}
