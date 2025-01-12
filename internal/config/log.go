package common

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func InitLogger(logLevel LogLevel) (*zap.Logger, error) {
	baseLoggerCfg := zap.NewProductionConfig()
	baseLoggerCfg.DisableStacktrace = true
	level, err := zapcore.ParseLevel(string(logLevel))
	if err != nil {
		return nil, fmt.Errorf("failed to parse level \"%s\": %w", logLevel, err)
	}
	baseLoggerCfg.Level = zap.NewAtomicLevelAt(level)
	return baseLoggerCfg.Build()
}
