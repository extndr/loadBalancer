package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
)

func Run(addr string, handler http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: handler}

	errChan := make(chan error, 1)

	go func() {
		log.Info("──────────────────────────────────────────────")
		log.Infof("Load balancer started on %s", addr)
		log.Info("──────────────────────────────────────────────")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("server failed: %w", err)
		}
	}()

	// Wait for interrupt signal to gracefully shut down the server
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case <-stop:
		log.Info("Shutting down gracefully...")
	case err := <-errChan:
		return err
	}

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
