package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultListenAddress          = ":8080"
	defaultAccountID              = "default"
	defaultDatabaseMaxConnections = int32(20)
)

type Config struct {
	Server    ServerConfig      `yaml:"server"`
	Database  DatabaseConfig    `yaml:"database"`
	AccountID string            `yaml:"account_id"`
	Auth      map[string]string `yaml:"auth"`
}

type ServerConfig struct {
	ListenAddress string `yaml:"listen_address"`
}

type DatabaseConfig struct {
	DSN            string `yaml:"dsn"`
	MaxConnections int32  `yaml:"max_connections"`
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()

	config := Config{}
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode config %q: %w", path, err)
	}

	config.Server.ListenAddress = strings.TrimSpace(config.Server.ListenAddress)
	if config.Server.ListenAddress == "" {
		config.Server.ListenAddress = defaultListenAddress
	}
	config.Database.DSN = strings.TrimSpace(config.Database.DSN)
	if config.Database.MaxConnections == 0 {
		config.Database.MaxConnections = defaultDatabaseMaxConnections
	}
	config.AccountID = strings.TrimSpace(config.AccountID)
	if config.AccountID == "" {
		config.AccountID = defaultAccountID
	}

	if config.Database.DSN == "" {
		return Config{}, errors.New("database.dsn is required")
	}
	if config.Database.MaxConnections < 1 || config.Database.MaxConnections > 200 {
		return Config{}, errors.New("database.max_connections must be between 1 and 200")
	}
	if len(config.Auth) == 0 {
		return Config{}, errors.New("auth must contain at least one token")
	}

	secrets := make(map[string]string, len(config.Auth))
	normalizedAuth := make(map[string]string, len(config.Auth))
	for rawName, rawSecret := range config.Auth {
		name := strings.TrimSpace(rawName)
		secret := rawSecret
		if name == "" {
			return Config{}, errors.New("auth token name must not be empty")
		}
		if strings.TrimSpace(secret) == "" {
			return Config{}, fmt.Errorf("auth.%s must not be empty", name)
		}
		if _, exists := normalizedAuth[name]; exists {
			return Config{}, fmt.Errorf("auth token name %q is duplicated", name)
		}
		if existingName, exists := secrets[secret]; exists {
			return Config{}, fmt.Errorf("auth.%s and auth.%s use the same secret", existingName, name)
		}
		normalizedAuth[name] = secret
		secrets[secret] = name
	}
	config.Auth = normalizedAuth

	return config, nil
}
