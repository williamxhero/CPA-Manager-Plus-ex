package proxy

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

func isLegacyQuotaRequest(r *http.Request) (bool, error) {
	if strings.Contains(r.URL.Path, "/quota") {
		return true, nil
	}
	if !strings.HasSuffix(r.URL.Path, "/api-call") || r.Method != "POST" {
		return false, nil
	}
	body, err := readAndRestoreRequestBody(r, 32<<10)
	if err != nil {
		return false, err
	}
	var request struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(body, &request) != nil {
		return false, nil
	}
	u, err := url.Parse(request.URL)
	if err != nil {
		return false, nil
	}
	path := strings.TrimRight(u.Path, "/")
	switch path {
	case "/backend-api/wham/usage", "/user/balance", "/payment/checklist", "/api/usage/token", "/api/user/self", "/zen/go/v1/usage", "/inference/go/v1/usage":
		return true, nil
	}
	return false, nil
}
