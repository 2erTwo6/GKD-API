package config

import "os"

type Config struct {
	Port          string
	DBPath        string
	AdminPassword string
}

func Load() *Config {
	return &Config{
		Port:          getEnv("GKD_PORT", "8787"),
		DBPath:        getEnv("GKD_DB", "gkd-api.db"),
		AdminPassword: os.Getenv("GKD_ADMIN_PASSWORD"),
	}
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
