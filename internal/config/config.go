// Package config loads service configuration from the environment. Credentials
// and athlete ids are never hardcoded; they come from environment variables,
// optionally seeded from a local .env file in development.
package config

import (
	"fmt"
	"os"
	"strings"
)

// RequireEnv returns a required environment variable, or an error explaining how
// to set it.
func RequireEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf(
			"environment variable %s is not set: export it, or set it in .env for local "+
				"development (see .env.example; production uses the systemd EnvironmentFile)", name)
	}
	return value, nil
}

// LoadDotEnv reads key=value pairs from a .env file in the working directory, if
// present, without overriding variables already set in the environment. It is
// best-effort and silently ignores a missing file. Production deployments set
// real environment variables (via systemd EnvironmentFile) and need no .env.
func LoadDotEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
}
