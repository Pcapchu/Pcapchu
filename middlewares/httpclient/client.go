package httpclient

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

// NewResilientClient returns an *http.Client with connection-level timeouts,
// automatic retries on transient failures, and exponential backoff.
func NewResilientClient() *http.Client {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
	}

	rc := retryablehttp.NewClient()
	rc.HTTPClient = &http.Client{Transport: transport}
	rc.RetryMax = 3
	rc.RetryWaitMin = 1 * time.Second
	rc.RetryWaitMax = 5 * time.Second
	rc.Logger = nil // silence default logger

	return &http.Client{
		Transport: &retryablehttp.RoundTripper{Client: rc},
		Timeout:   120 * time.Second,
	}
}
