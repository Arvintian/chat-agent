package providers

import "net/http"

// headerTransport injects custom headers into every HTTP request.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}

func newHeaderClient(headers map[string]string) *http.Client {
	return &http.Client{
		Transport: &headerTransport{
			base:    http.DefaultTransport,
			headers: headers,
		},
	}
}
