package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/extndr/loadBalancer/config"
	"github.com/joho/godotenv"
	log "github.com/sirupsen/logrus"
)

type LoadBalancer struct {
	backends []*url.URL
	counter  uint64
	timeout  time.Duration
	client   *http.Client
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Warn(".env file not found, using defaults")
	}

	cfg := &config.LBConfig{
		Port:     config.GetPort(),
		Backends: config.GetBackends(),
	}

	if err := runServer(cfg); err != nil {
		log.Fatalf("Server exited with error: %v", err)
	}
}

func runServer(cfg *config.LBConfig) error {
	lb, err := NewLB(cfg.Backends, nil)
	if err != nil {
		log.Fatalf("failed to create LoadBalancer: %v", err)
	}

	srv := &http.Server{Addr: cfg.Port, Handler: lb}

	// Start the HTTP server in a separate goroutine so that
	// main can continue running and handle graceful shutdown
	// signals (Ctrl+C or SIGTERM)
	go func() {
		log.Info("──────────────────────────────────────────────")
		log.Infof("Load balancer started on %s", cfg.Port)
		log.Info("Backends configured:")
		for idx, b := range cfg.Backends {
			log.Infof("[%d] %s", idx+1, b)
		}
		log.Info("──────────────────────────────────────────────")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shut down the server
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Info("Shutting down gracefully...")
	// Give in-flight requests up to 5 seconds to complete before forcing shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	log.Info("──────────────────────────────────────────────")
	log.Info("Server stopped cleanly. Goodbye!")
	log.Info("──────────────────────────────────────────────")

	return nil
}

// NewLB creates a new LoadBalancer.
// backends — a list of backend URLs.
// timeout — the maximum duration to wait for a backend response.
// If nil is passed, a default timeout of 5 seconds is used.
func NewLB(backends []string, timeout *time.Duration) (*LoadBalancer, error) {
	if len(backends) < 2 {
		return nil, errors.New("at least 2 backends are required for load balancing")
	}

	var parsedURLs []*url.URL
	for _, b := range backends {
		u, err := url.Parse(b)
		if err != nil {
			return nil, fmt.Errorf("invalid backend URL %q: %w", b, err)
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
