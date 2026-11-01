package balance

import (
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/queryfeedback"
	"net/http"
)

// Metadata only: no adapter arithmetic, credentials, units or inference changes.
type retryAfterTransport struct{ *http.Transport }

func (t retryAfterTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	res, err := t.Transport.RoundTrip(r)
	if err == nil && res.StatusCode == 429 {
		queryfeedback.RecordRetry(r.Context(), res.Header.Get("Retry-After"))
	}
	return res, err
}
