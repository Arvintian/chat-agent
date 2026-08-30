package providers

import (
	"net/http"
	"time"
)

// defaultIdleTimeout is the default connection pool idle timeout used by the
// shared HTTP client when no idle timeout is configured.
const defaultIdleTimeout = 30 * time.Second

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

// newHTTPClient builds the HTTP client shared by all providers. It applies a
// request timeout (if > 0), a connection-pool idle timeout (idle, default 30s)
// and injects custom headers (if any) into every request.
func newHTTPClient(headers map[string]string, timeout, idle time.Duration) *http.Client {
	if idle <= 0 {
		idle = defaultIdleTimeout
	}
	baseTransport := &http.Transport{
		IdleConnTimeout: idle,
	}
	var transport http.RoundTripper = baseTransport
	if len(headers) > 0 {
		transport = &headerTransport{base: baseTransport, headers: headers}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}
