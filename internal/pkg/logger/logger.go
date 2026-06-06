package logger

import (
	"os"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func Init(serviceName string) {
	zerolog.TimeFieldFormat = time.RFC3339

	var globalLogger zerolog.Logger

	if config.AppConfig.AppEnv == config.Development {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		consoleWriter := zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: "15:04:05",
		}
		globalLogger = zerolog.New(consoleWriter).With().
			Timestamp().
			Caller().
			Str("service", serviceName).
			Logger()
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
		globalLogger = zerolog.New(os.Stdout).With().
			Timestamp().
			Caller().
			Str("service", serviceName).
			Logger()
	}

	log.Logger = globalLogger
}
