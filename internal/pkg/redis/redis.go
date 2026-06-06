package redis

import (
	"context"
	"fmt"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/redis/go-redis/v9"
)

var Client *redis.Client

func Init() {
	cfg := config.AppConfig
	Client = redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Redis_Host, cfg.Redis_Port),
		Password: cfg.Redis_Password,
		DB:       cfg.Redis_DB,
	})

	if err := Client.Ping(context.Background()).Err(); err != nil {
		panic(fmt.Sprintf("failed to connect to redis: %v", err))
	}
}
