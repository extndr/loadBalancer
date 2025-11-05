package config

import (
	"os"
	"strings"
)

type LBConfig struct {
	Port     string
	Backends []string
}

func GetPort() string {
	if port := os.Getenv("PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}

func GetBackends() []string {
	if backends := os.Getenv("BACKENDS"); backends != "" {
		parts := strings.Split(backends, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}
	return []string{
		"http://localhost:8081",
		"http://localhost:8082",
		"http://localhost:8083",
	}
}
