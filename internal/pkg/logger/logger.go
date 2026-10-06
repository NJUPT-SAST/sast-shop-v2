package logger

import (
	"os"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func Init(serviceName string) {
	// 2025-01-02T15:04:05+08:00
	zerolog.TimeFieldFormat = time.RFC3339

	var globalLogger zerolog.Logger

	if config.AppConfig.AppEnv == config.Development {
		// 设置全局最低日志级别为debug
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		// 创建控制台输出器，输出到标准输出 stdout，时间只显示时分秒
		consoleWriter := zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: "15:04:05",
		}
		globalLogger = zerolog.New(consoleWriter).With().
			Timestamp().
			Caller(). // Caller()：每条日志带调用位置，例如文件名和行号。
			Str("service", serviceName).
			Logger() // 构建最终 logger。
	} else {
		// 生产环境，最低级别设置为 Info，所以 Debug 日志不会输出。
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
		globalLogger = zerolog.New(os.Stdout).With().
			Timestamp().
			Caller().
			Str("service", serviceName).
			Logger()
	}

	log.Logger = globalLogger
}
