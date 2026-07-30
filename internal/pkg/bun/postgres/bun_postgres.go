package postgres

import (
	"database/sql"
	"fmt"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

var DB *bun.DB

func Init() {
	cfg := config.AppConfig
	dsn := fmt.Sprintf(
		"postgresql://%s:%s@%s:%d/%s?sslmode=disable",
		cfg.DB_Username, cfg.DB_Password, cfg.DB_Host, cfg.DB_Port, cfg.DB_Name,
	)
	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
	DB = bun.NewDB(sqldb, pgdialect.New())
}
