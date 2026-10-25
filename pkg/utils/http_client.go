package utils

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	appMaxIdleConns        = 4
	appMaxIdleConnsPerHost = 2
	appIdleConnTimeout     = 30 * time.Second
)

// newBoundedTransport returns a transport with a small idle-connection pool.
// It clones http.DefaultTransport to preserve TLS, HTTP/2 and dial timeouts.
func newBoundedTransport() *http.Transport {
	var tr *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		tr = base.Clone()
	} else {
		tr = &http.Transport{}
	}
	tr.MaxIdleConns = appMaxIdleConns
	tr.MaxIdleConnsPerHost = appMaxIdleConnsPerHost
	tr.IdleConnTimeout = appIdleConnTimeout
	tr.DisableCompression = false
	tr.TLSHandshakeTimeout = 15 * time.Second
	return tr
}

// CreateHTTPClient creates an HTTP client with optional proxy support.
// If proxyURL is empty, it uses the system environment proxy settings.
// Supported proxy schemes: http, https, socks5, socks5h.
func CreateHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	tr := newBoundedTransport()

	if proxyURL != "" {
		proxy, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %w", err)
		}
		scheme := strings.ToLower(proxy.Scheme)
		switch scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, fmt.Errorf(
				"unsupported proxy scheme %q (supported: http, https, socks5, socks5h)",
				proxy.Scheme,
			)
		}
		if proxy.Host == "" {
			return nil, fmt.Errorf("invalid proxy URL: missing host")
		}
		tr.Proxy = http.ProxyURL(proxy)
	} else {
		tr.Proxy = http.ProxyFromEnvironment
	}

	return &http.Client{Timeout: timeout, Transport: tr}, nil
}
