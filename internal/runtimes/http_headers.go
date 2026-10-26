package runtimes

import (
	"net/http"
	"strings"
)

// providerTransport keeps GitHub's vendor media type scoped to the GitHub API.
// Other runtime providers (notably Adoptium) expect a normal JSON Accept header
// and may reject application/vnd.github+json with HTTP 406.
type providerTransport struct {
	base http.RoundTripper
}

func (t providerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	if !strings.EqualFold(clone.URL.Hostname(), "api.github.com") {
		clone.Header.Set("Accept", "application/json")
	}
	return t.base.RoundTrip(clone)
}

func init() {
	base := httpClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	httpClient.Transport = providerTransport{base: base}
}
