// Package bss implements the Alibaba Cloud billing RPC API without an SDK.
package bss

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"sort"
	"strings"
)

// PercentEncode encodes UTF-8 bytes using RFC3986, not form encoding.
func PercentEncode(value string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.~", rune(c)) {
			out.WriteByte(c)
		} else {
			out.WriteByte('%')
			out.WriteByte(hex[c>>4])
			out.WriteByte(hex[c&15])
		}
	}
	return out.String()
}

// CanonicalQuery sorts raw parameter keys and encodes each raw key and value.
// Signature is excluded, permitting verification of an already signed query.
func CanonicalQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		if key != "Signature" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, PercentEncode(key)+"="+PercentEncode(params[key]))
	}
	return strings.Join(parts, "&")
}

func StringToSign(params map[string]string) string {
	return "GET&%2F&" + PercentEncode(CanonicalQuery(params))
}

func Sign(params map[string]string, accessKeySecret string) string {
	mac := hmac.New(sha1.New, []byte(accessKeySecret+"&"))
	_, _ = mac.Write([]byte(StringToSign(params)))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
