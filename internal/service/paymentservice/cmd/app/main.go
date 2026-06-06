package main

import (
	"fmt"
	"log"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/logger"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/redis"
	v1 "github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/handler/v1"
	"github.com/labstack/echo/v5"
)

func main() {
	config.Init()
	logger.Init(constant.PaymentServiceName)
	postgres.Init()
	redis.Init()
	e := echo.New()
	v1.Init(e)
	if err := e.Start(fmt.Sprintf(":%d", config.AppConfig.PaymentServicePort)); err != nil {
		log.Fatal(err)
	}
}
