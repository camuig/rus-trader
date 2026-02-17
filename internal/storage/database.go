package storage

import (
	"fmt"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func NewDatabase(dbPath string) (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}

	// Enable WAL mode for concurrent read/write
	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	if err := db.AutoMigrate(&Trade{}, &AnalysisLog{}, &PortfolioSnapshot{}); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}

	// Migrate data from old pn_l column (GORM default for PnL) to explicit pnl column
	var hasPnL int
	sqlDB.QueryRow("SELECT COUNT(*) FROM pragma_table_info('trades') WHERE name = 'pn_l'").Scan(&hasPnL)
	if hasPnL > 0 {
		sqlDB.Exec("UPDATE trades SET pnl = pn_l WHERE pn_l != 0 AND (pnl IS NULL OR pnl = 0)")
	}

	return db, nil
}
