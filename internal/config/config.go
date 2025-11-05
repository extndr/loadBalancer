package config

import (
	"os"
	"strings"
)

type LBConfig struct {
	Port     string
	Backends []string
}

func LoadConfig() *LBConfig {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	port = ":" + port

	backends := os.Getenv("BACKENDS")
	var backendList []string
	if backends == "" {
		backendList = []string{
			"http://localhost:8081",
			"http://localhost:8082",
			"http://localhost:8083",
		}
	} else {
		for b := range strings.SplitSeq(backends, ",") {
			backendList = append(backendList, strings.TrimSpace(b))
		}
	}

	return &LBConfig{
		Port:     port,
		Backends: backendList,
	}
}
