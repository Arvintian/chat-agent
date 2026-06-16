package serve

import (
	"net/http"
	"time"
)

// Default values for client configuration.
const (
	DefaultReconnectDelay = 2 * time.Second
	DefaultMaxReconnect   = 5
	DefaultWriteTimeout   = 5 * time.Second
	DefaultReadTimeout    = 10 * time.Second
)

// ClientOption is a functional option for configuring the Client.
type ClientOption func(*Client)

// WithHTTPHeader sets a custom HTTP header for the WebSocket upgrade request.
func WithHTTPHeader(key, value string) ClientOption {
	return func(c *Client) {
		if c.headers == nil {
			c.headers = make(http.Header)
		}
		c.headers.Set(key, value)
	}
}

// WithHTTPHeaders sets multiple custom HTTP headers for the WebSocket upgrade request.
func WithHTTPHeaders(headers http.Header) ClientOption {
	return func(c *Client) {
		c.headers = headers.Clone()
	}
}

// WithBasicAuth sets Basic Authentication credentials for the WebSocket connection.
func WithBasicAuth(username, password string) ClientOption {
	return func(c *Client) {
		c.basicAuthUser = username
		c.basicAuthPass = password
	}
}

// WithReconnect enables automatic reconnection with the given delay between attempts.
// maxAttempts controls how many reconnection attempts are made (0 = unlimited).
func WithReconnect(delay time.Duration, maxAttempts int) ClientOption {
	return func(c *Client) {
		c.reconnectDelay = delay
		c.maxReconnect = maxAttempts
	}
}

// WithSessionID sets a specific session ID for the connection.
// If not set, the server will generate one automatically.
func WithSessionID(sessionID string) ClientOption {
	return func(c *Client) {
		c.sessionID = sessionID
	}
}

// WithWriteTimeout sets the write deadline for WebSocket writes.
func WithWriteTimeout(d time.Duration) ClientOption {
	return func(c *Client) {
		c.writeTimeout = d
	}
}

// WithReadTimeout sets the read deadline for WebSocket reads.
func WithReadTimeout(d time.Duration) ClientOption {
	return func(c *Client) {
		c.readTimeout = d
	}
}
