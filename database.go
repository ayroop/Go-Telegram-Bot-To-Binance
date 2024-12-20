// database.go
package main

import (
	"fmt"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var db *gorm.DB

func initDatabase() error {
	var err error
	db, err = gorm.Open(sqlite.Open("config.db"), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// Migrate the schema
	if err := db.AutoMigrate(&Config{}); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	return nil
}