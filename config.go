// config.go
package main

import (
	"errors"
	"fmt"
	"sync"

	"gorm.io/gorm"
)

var (
	GlobalConfig Config
	configMutex  = &sync.RWMutex{}
)

// Config holds the application configuration.
type Config struct {
	ID               uint `gorm:"primaryKey"`
	TelegramBotToken string
	TelegramChatID   int64
}

// Validate checks the Config fields for validity.
func (config *Config) Validate() error {
	if config.TelegramBotToken == "" {
		return errors.New("Telegram bot token cannot be empty")
	}
	if config.TelegramChatID == 0 {
		return errors.New("Telegram chat ID cannot be zero")
	}
	return nil
}

// getConfig loads the configuration from the database.
func getConfig() (*Config, error) {
	var config Config
	if err := db.First(&config, 1).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("failed to retrieve configuration: %w", err)
	}
	return &config, nil
}

// saveConfig saves the configuration to the database.
func saveConfig(config *Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	config.ID = 1 // Fixed ID to enforce single record

	if err := db.Save(config).Error; err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}
	return nil
}

// GetGlobalConfig safely retrieves the GlobalConfig
func GetGlobalConfig() Config {
	configMutex.RLock()
	defer configMutex.RUnlock()
	return GlobalConfig
}

// SetGlobalConfig safely sets the GlobalConfig
func SetGlobalConfig(config Config) {
	configMutex.Lock()
	defer configMutex.Unlock()
	GlobalConfig = config
}