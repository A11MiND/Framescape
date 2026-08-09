package persistence

import (
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Config is the subset of connection settings needed to open the DB. Loaded
// from env by cmd/api and cmd/scheduler's own main.go, not by this package.
type Config struct {
	DSN string // e.g. "user:pass@tcp(127.0.0.1:3306)/aigc?parseTime=true&loc=UTC&charset=utf8mb4"
}

// Open connects to MySQL. AutoMigrate is never called (PRD §15.1) — schema
// changes go through goose (migrations/*.sql) exclusively.
func Open(cfg Config) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(cfg.DSN), &gorm.Config{
		Logger:                 gormlogger.Default.LogMode(gormlogger.Warn),
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	return db, nil
}
