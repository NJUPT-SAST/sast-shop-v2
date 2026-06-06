package config

import (
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Environment string

var (
	Development = Environment(constant.Dev)
	Test        = Environment(constant.Test)
	Production  = Environment(constant.Prod)
)

type Config struct {
	// Database configuration
	DB_Username string `env:"DB_USERNAME" envDefault:"root"`
	DB_Password string `env:"DB_PASSWORD" envDefault:"password"`
	DB_Host     string `env:"DB_HOST"     envDefault:"localhost"`
	DB_Port     int32  `env:"DB_PORT"     envDefault:"5432"`
	DB_Name     string `env:"DB_NAME"     envDefault:"sast_shop_v2"`

	// Redis configuration
	Redis_Host     string `env:"REDIS_HOST"     envDefault:"localhost"`
	Redis_Port     int32  `env:"REDIS_PORT"     envDefault:"6379"`
	Redis_Password string `env:"REDIS_PASSWORD" envDefault:""`
	Redis_DB       int    `env:"REDIS_DB"       envDefault:"0"`

	// Feishu configuration
	Feishu_AppID     string `env:"FEISHU_APP_ID"     envDefault:"your_feishu_app_id"`
	Feishu_AppSecret string `env:"FEISHU_APP_SECRET" envDefault:"your_feishu_app_secret"`

	// App configuration
	AppEnv Environment `env:"APP_ENV" envDefault:"development"`

	// Service ports
	UserServicePort    int32 `env:"USER_SERVICE_PORT"    envDefault:"1323"`
	CatalogServicePort int32 `env:"CATALOG_SERVICE_PORT" envDefault:"1324"`
	PaymentServicePort int32 `env:"PAYMENT_SERVICE_PORT" envDefault:"1325"`
	SpotServicePort    int32 `env:"SPOT_SERVICE_PORT"    envDefault:"1326"`
	ErrandServicePort  int32 `env:"ERRAND_SERVICE_PORT"  envDefault:"1327"`
}

var AppConfig *Config

func Init() {
	_ = godotenv.Load(".env", "../.env", "../../.env", "../../../.env") //nolint:errcheck
	var config Config
	if err := env.Parse(&config); err != nil {
		panic(err)
	}
	AppConfig = &config
}
