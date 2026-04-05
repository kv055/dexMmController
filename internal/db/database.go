package db

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
)

// InitDB initializes the database connection, runs migrations, and auto-migrates models
func InitDB(dbPath string) (*gorm.DB, error) {
	// Open SQLite database
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Run Goose migrations
	if err := goose.SetDialect("sqlite3"); err != nil {
		return nil, fmt.Errorf("failed to set goose dialect: %w", err)
	}

	if err := goose.Up(sqlDB, "./migrations"); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Println("✓ Migrations applied successfully")

	// Connect GORM to the database
	db, err := gorm.Open(sqlite.Dialector{Conn: sqlDB}, &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database with GORM: %w", err)
	}

	// Auto-migrate GORM models (creates tables if they don't exist)
	if err := db.AutoMigrate(&Transaction{}, &WalletAddress{}); err != nil {
		return nil, fmt.Errorf("failed to auto-migrate models: %w", err)
	}

	log.Println("✓ Database initialized successfully")
	return db, nil
}

// Close closes the database connection
func Close(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}
	return sqlDB.Close()
}
