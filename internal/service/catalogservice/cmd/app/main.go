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

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	config.Init()
	cfg := config.AppConfig
	if cfg.CatalogAdvertiseAddress == "" {
		return fmt.Errorf("CATALOG_ADVERTISE_ADDRESS is required")
	}

	logger.Init(constant.CatalogServiceName)
	log.Printf("[catalog] Consul address: %s", cfg.ConsulAddress)
	log.Printf(
		"[catalog] register name=%s instance_id=%s advertise=%s port=%d",
		cfg.CatalogConsulName,
		cfg.CatalogInstanceID,
		cfg.CatalogAdvertiseAddress,
		cfg.CatalogServicePort,
	)
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
	consulConfig.Address = cfg.ConsulAddress
	consulConfig.HttpClient = &http.Client{Timeout: 5 * time.Second}

	consulClient, err := api.NewClient(consulConfig)
	if err != nil {
		return fmt.Errorf("create consul client: %w", err)
	}
	if err := discovery.Register(
		consulClient,
		cfg.CatalogInstanceID,
		cfg.CatalogConsulName,
		cfg.CatalogAdvertiseAddress,
		int(cfg.CatalogServicePort),
	); err != nil {
		return fmt.Errorf("register catalog service:  %w", err)
	}

	defer func() {
		if err := discovery.Deregister(consulClient, cfg.CatalogInstanceID); err != nil {
			log.Printf("[catalog] deregister failed: %v", err)
			return
		}
		log.Printf("[catalog] deregistered instance %s", cfg.CatalogInstanceID)
	}()

	return e.Start(fmt.Sprintf(":%d", cfg.CatalogServicePort))
}
