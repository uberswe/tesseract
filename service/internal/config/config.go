package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
)

type Config struct {
	TCPPort            string
	HealthPort         string
	DatabaseURL        string
	PersistIntervalSec int
	PingIntervalSec    int
	PingTimeoutSec     int
	MaxPacketSize      int
}

func Load() *Config {
	return &Config{
		TCPPort:            getEnv("TCP_PORT", "7600"),
		HealthPort:         getEnv("HEALTH_PORT", "8081"),
		DatabaseURL:        buildDatabaseURL(),
		PersistIntervalSec: getEnvInt("PERSIST_INTERVAL_SECONDS", 30),
		PingIntervalSec:    getEnvInt("PING_INTERVAL_SECONDS", 15),
		PingTimeoutSec:     getEnvInt("PING_TIMEOUT_SECONDS", 10),
		MaxPacketSize:      getEnvInt("MAX_PACKET_SIZE", 262144),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func buildDatabaseURL() string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	host := getEnv("DB_HOST", "localhost")
	port := getEnv("DB_PORT", "5432")
	user := getEnv("DB_USER", "gearworks")
	pass := getEnv("DB_PASSWORD", "changeme_pg_dev")
	name := getEnv("DB_NAME", "gearworks")
	sslmode := getEnv("DB_SSLMODE", "disable")
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		url.PathEscape(user), url.PathEscape(pass), host, port, name, sslmode)
}
