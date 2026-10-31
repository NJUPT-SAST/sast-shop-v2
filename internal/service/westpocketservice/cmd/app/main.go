package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/feishu"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/logger"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/redis"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/westpocket"
	"github.com/labstack/echo/v5"
)

func main() {
	config.Init()
	logger.Init("westpocketservice")
	postgres.Init()
	redis.Init("westpocketservice")
	if config.AppConfig.Feishu_AppID != "" && config.AppConfig.Feishu_AppSecret != "" &&
		config.AppConfig.Feishu_AppID != constant.FeishuDefaultAppID &&
		config.AppConfig.Feishu_AppSecret != constant.FeishuDefaultAppSecret {
		feishu.Init()
	}
	service, err := westpocket.NewService(postgres.DB)
	if err != nil {
		log.Fatal(err)
	}
	e := echo.New()
	if err = westpocket.Register(e, service); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	service.Run(ctx)
	port := 1328
	if raw := os.Getenv("WEST_POCKET_SERVICE_PORT"); raw != "" {
		port, err = strconv.Atoi(raw)
		if err != nil || port <= 0 || port > 65535 {
			log.Fatal("invalid WEST_POCKET_SERVICE_PORT")
		}
	}
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           e,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       45 * time.Second,
		WriteTimeout:      45 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Printf("shutdown failed: %v", err)
		}
	}()
	if err = server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
