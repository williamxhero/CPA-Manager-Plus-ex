package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// DeepInfraPayerReference is private, stable across Manager restarts using
// the same data key, and cannot be used to recover a low-entropy account ID.
func (p *Protector) DeepInfraPayerReference(root string) string {
	if p == nil || root == "" {
		return ""
	}
	m := hmac.New(sha256.New, p.key)
	_, _ = m.Write([]byte("cpa-official/deepinfra/payer/v1\n" + root))
	return hex.EncodeToString(m.Sum(nil)[:16])
}
