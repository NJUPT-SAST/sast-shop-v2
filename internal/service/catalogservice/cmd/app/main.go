package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/cos"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/discovery"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/feishu"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/logger"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/redis"
	v1 "github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/handler/v1"
	catalogservice "github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/service"
	catalogstorage "github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/storage"
	"github.com/hashicorp/consul/api"
	"github.com/labstack/echo/v5"
	zerolog "github.com/rs/zerolog/log"
)

func main() {
	config.Init()
	logger.Init(constant.CatalogServiceName)
	postgres.Init()
	redis.Init(constant.CatalogServiceName)
	feishu.Init()
	cos.Init()
	e := echo.New()
	e.GET("/health/live", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	store, err := catalogstorage.NewCosStore()
	if err == nil {
		uploader := catalogservice.NewProductImageUploadService(
			store,
			"",
			catalogservice.ImageLimits{},
			redis.NewUploadQuota(catalogservice.DefaultDailyQuotaBytes),
		)
		v1.Init(e, v1.UploadDependencies{Uploader: uploader})
	} else {
		zerolog.Warn().Err(err).Msg("cos not initialized; product image upload disabled")
		v1.Init(e)
	}

	// Register before e.Start because it blocks while serving requests.
	consulConfig := api.DefaultConfig()
	consulConfig.Address = "127.0.0.1:8500"
	consulConfig.HttpClient = &http.Client{Timeout: 5 * time.Second}

	consulClient, err := api.NewClient(consulConfig)
	if err != nil {
		log.Fatalf("create consul client: %v", err)
	}
	if err := discovery.Register(
		consulClient,
		"shop-catalog-local-1",
		"shop-catalog",
		"172.19.0.1",
		int(config.AppConfig.CatalogServicePort),
	); err != nil {
		log.Fatalf("register catalog service: %v", err)
	}

	if err := e.Start(fmt.Sprintf(":%d", config.AppConfig.CatalogServicePort)); err != nil {
		log.Fatal(err)
	}
}
