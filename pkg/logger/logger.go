package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func New(service string) *zap.Logger {
	level := zapcore.InfoLevel
	if l := os.Getenv("LOG_LEVEL"); l != "" {
		_ = level.UnmarshalText([]byte(l))
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.Lock(os.Stdout),
		level,
	)

	return zap.New(core).With(zap.String("service", service))
}
