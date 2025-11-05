package lb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

type LoadBalancer struct {
	backends []*url.URL
	counter  uint64
	timeout  time.Duration
	client   *http.Client
}

// New creates a new LoadBalancer.
// backends — a list of backend URLs.
// timeout — the maximum duration to wait for a backend response.
// If nil is passed, a default timeout of 5 seconds is used.
func New(backends []string, timeout *time.Duration) (*LoadBalancer, error) {
	if len(backends) < 2 {
		return nil, errors.New("at least 2 backends are required for load balancing")
	}

	var parsedURLs []*url.URL
	for _, b := range backends {
		u, err := url.Parse(b)
		if err != nil {
			return nil, fmt.Errorf("invalid backend URL %q: %w", b, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("backend %q must use http/https", b)
		}
		parsedURLs = append(parsedURLs, u)
	}

	client := &http.Client{
		// Custom transport to optimize connection reuse and timeouts
		Transport: &http.Transport{
			MaxIdleConns:        30,
			MaxIdleConnsPerHost: 30,
			IdleConnTimeout:     90 * time.Second,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 10 * time.Second,
			}).DialContext,
		},
	}

	defaultTimeout := 5 * time.Second

	if timeout == nil {
		timeout = &defaultTimeout
	}

	return &LoadBalancer{
		backends: parsedURLs,
		client:   client,
		timeout:  *timeout,
	}, nil
}

// getNextBackend returns the next backend in round-robin order.
// Uses atomic counter to be safe for concurrent requests.
func (lb *LoadBalancer) getNextBackend() *url.URL {
	idx := atomic.AddUint64(&lb.counter, 1) - 1
	return lb.backends[idx%uint64(len(lb.backends))]
}

func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := lb.getNextBackend()

	// Apply timeout to backend requests to avoid hanging
	ctx, cancel := context.WithTimeout(r.Context(), lb.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, r.Method, target.String()+r.RequestURI, r.Body)
	if err != nil {
		log.Errorf("failed to create request for %s: %v", target.Host, err)
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}
	req.Header = r.Header.Clone()

	start := time.Now()
	resp, err := lb.client.Do(req)
	elapsed := time.Since(start)

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			log.Warnf("[timeout] %s did not respond within %ds", target.Host, int(elapsed.Seconds()))
			http.Error(
				w,
				fmt.Sprintf("Backend request timed out after %ds", int(elapsed.Seconds())),
				http.StatusGatewayTimeout,
			)
			return
		}

		log.Errorf("request to %s failed: %v", target.Host, err)
		http.Error(w, "Service temporarily unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	log.Infof("%s → %s %d %dms", r.Method, target.Host, resp.StatusCode, elapsed.Milliseconds())

	// Copy response headers from the backend to the client.
	// This preserves all headers (like Content-Type, Set-Cookie, etc.).
	for k, v := range resp.Header {
		for _, vv := range v {
			w.Header().Add(k, vv)
		}
	}

	// Write the backend status code to the client
	w.WriteHeader(resp.StatusCode)

	// Stream the backend response body to the client.
	// io.Copy handles large responses efficiently without loading them fully into memory.
	io.Copy(w, resp.Body)
}
