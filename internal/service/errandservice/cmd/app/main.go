package main

import (
	"fmt"
	"log"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/feishu"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/logger"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/redis"
	v1 "github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/handler/v1"
	"github.com/labstack/echo/v5"
)

func main() {
	config.Init()
	logger.Init(constant.ErrandServiceName)
	postgres.Init()
	redis.Init(constant.ErrandServiceName)
	feishu.Init()
	e := echo.New()
	v1.Init(e)
	if err := e.Start(fmt.Sprintf(":%d", config.AppConfig.ErrandServicePort)); err != nil {
		log.Fatal(err)
	}
}
