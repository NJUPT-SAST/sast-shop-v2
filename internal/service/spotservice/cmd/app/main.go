package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/discovery"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/feishu"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/logger"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/redis"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/client"
	v1 "github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/handler/v1"
	"github.com/hashicorp/consul/api"
	"github.com/labstack/echo/v5"
)

func main() {
	config.Init()
	logger.Init(constant.SpotServiceName)
	postgres.Init()
	redis.Init(constant.SpotServiceName)
	feishu.Init()
	consulConfig := api.DefaultConfig()
	consulConfig.Address = "127.0.0.1:8500"
	consulConfig.HttpClient = &http.Client{
		Timeout: time.Second * 10,
	}
	consulClient, err := api.NewClient(consulConfig)
	if err != nil {
		log.Fatal(err)
	}
	catalogURL, err := discovery.Resolve(consulClient, "shop-catalog")
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Catalog URL: %s", catalogURL)
	client.Init(catalogURL)
	e := echo.New()
	v1.Init(e)
	if err := e.Start(fmt.Sprintf(":%d", config.AppConfig.SpotServicePort)); err != nil {
		log.Fatal(err)
	}
}
