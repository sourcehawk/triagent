package prom

import "net/http"

// promClient is the thin HTTP client wrapper used by the catalog +
// tools. Real method set lands in Task 3 (catalog fetch).
type promClient struct {
	endpoint   string
	bearer     string
	basic      string
	httpClient *http.Client
}

func newPromClient(endpoint, bearer, basic string, c *http.Client) *promClient {
	return &promClient{endpoint: endpoint, bearer: bearer, basic: basic, httpClient: c}
}
