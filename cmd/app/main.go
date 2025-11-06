package main

import (
	log "github.com/sirupsen/logrus"

	"github.com/extndr/loadBalancer/internal/config"
	"github.com/extndr/loadBalancer/internal/lb"
	"github.com/extndr/loadBalancer/internal/server"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Warn(".env file not found, using defaults")
	}

	cfg := config.LoadConfig()

	lbInstance, err := lb.New(cfg.Backends, nil)
	if err != nil {
		log.WithError(err).Fatal("failed to create load balancer")
	}

	if err := server.Run(cfg.Port, lbInstance); err != nil {
		log.WithError(err).Fatal("server exited with error")
	}
}
