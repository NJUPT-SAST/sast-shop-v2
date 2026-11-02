package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/feishu"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/logger"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/redis"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/client"
	v1 "github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/handler/v1"
	consulapi "github.com/hashicorp/consul/api"
	"github.com/labstack/echo/v5"
)

func main() {
	config.Init()
	logger.Init(constant.SpotServiceName)
	postgres.Init()
	redis.Init(constant.SpotServiceName)
	feishu.Init()
	cfg := config.AppConfig
	consulConfig := consulapi.DefaultConfig()
	consulConfig.Address = cfg.ConsulAddress
	consulConfig.HttpClient = &http.Client{
		Timeout: time.Second * 10,
	}
	consulClient, err := consulapi.NewClient(consulConfig)
	if err != nil {
		log.Fatalf("create consul client: %v", err)
	}
	client.Init(consulClient, cfg.CatalogConsulName)
	e := echo.New()
	v1.Init(e)
	if err := e.Start(fmt.Sprintf(":%d", cfg.SpotServicePort)); err != nil {
		log.Fatal(err)
	}
}
