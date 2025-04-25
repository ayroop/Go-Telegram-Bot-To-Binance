package main

import (
	"fmt"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var db *gorm.DB

// Signal represents a trading signal stored in the database.
type Signal struct {
	ID         uint   `gorm:"primaryKey"`
	SignalID   string `gorm:"uniqueIndex"`
	EntryPrice float64
	TP1        float64
	TP2        float64
	TP3        float64
	SL         float64
	Timestamp  time.Time `gorm:"autoCreateTime"`
}

// Trade represents a trade result stored in the database.
type Trade struct {
	ID         uint   `gorm:"primaryKey"`
	SignalID   string `gorm:"index"`
	EntryPrice float64
	ExitPrice  float64
	Profit     float64
	Timestamp  time.Time `gorm:"autoCreateTime"`
}

// initDatabase initializes the database connection and migrates the schema.
func initDatabase() error {
	var err error
	db, err = gorm.Open(sqlite.Open("config.db"), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// Migrate the schema
	if err := db.AutoMigrate(&Config{}, &Signal{}, &Trade{}); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	return nil
}

// StoreSignal saves a trading signal to the database.
func StoreSignal(signalID string, entryPrice, tp1, tp2, tp3, sl float64) error {
	signal := Signal{
		SignalID:   signalID,
		EntryPrice: entryPrice,
		TP1:        tp1,
		TP2:        tp2,
		TP3:        tp3,
		SL:         sl,
	}

	if err := db.Create(&signal).Error; err != nil {
		return fmt.Errorf("failed to store signal: %w", err)
	}
	return nil
}

// StoreTrade saves a trade result to the database.
func StoreTrade(signalID string, entryPrice, exitPrice, profit float64) error {
	trade := Trade{
		SignalID:   signalID,
		EntryPrice: entryPrice,
		ExitPrice:  exitPrice,
		Profit:     profit,
	}

	if err := db.Create(&trade).Error; err != nil {
		return fmt.Errorf("failed to store trade: %w", err)
	}
	return nil
}

// GetTradesForPeriod retrieves trades from the database for a given period.
func GetTradesForPeriod(period string) ([]Trade, error) {
	var trades []Trade
	startTime := calculateStartTime(period)

	if err := db.Where("timestamp >= ?", startTime).Find(&trades).Error; err != nil {
		return nil, fmt.Errorf("failed to retrieve trades: %w", err)
	}

	return trades, nil
}

// calculateStartTime calculates the start time for a given period.
func calculateStartTime(period string) time.Time {
	now := time.Now()
	switch period {
	case "day":
		return now.AddDate(0, 0, -1)
	case "week":
		return now.AddDate(0, 0, -7)
	case "month":
		return now.AddDate(0, -1, 0)
	case "year":
		return now.AddDate(-1, 0, 0)
	default:
		return now
	}
}
