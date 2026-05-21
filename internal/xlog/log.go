package xlog

import (
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Logger struct {
	*zap.Logger
}

type Config struct {
	Level      string `yaml:"level"`       // 日志级别: debug, info, warn, error
	FilePath   string `yaml:"file_path"`   // 日志文件路径
	MaxSize    int    `yaml:"max_size"`    // 单个文件最大大小(MB)
	MaxBackups int    `yaml:"max_backups"` // 保留旧文件最大数量
	MaxAge     int    `yaml:"max_age"`     // 保留旧文件最大天数
	Compress   bool   `yaml:"compress"`    // 是否压缩旧文件
}

// Init 使用配置列表初始化多个具名 logger。
func New(cfg Config) (*Logger, error) {
	cfgEnc := zap.NewProductionEncoderConfig()
	cfgEnc.EncodeTime = zapcore.ISO8601TimeEncoder

	if cfg.FilePath != "" {
		dir := filepath.Dir(cfg.FilePath)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, err
			}
		}
	}

	lumberJackLogger := &lumberjack.Logger{
		Filename:   cfg.FilePath,
		MaxSize:    cfg.MaxSize,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAge,
		Compress:   cfg.Compress,
		LocalTime:  true,
	}

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(cfgEnc),
		zapcore.AddSync(lumberJackLogger),
		getZapLevel(cfg.Level),
	)
	l := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zap.PanicLevel), zap.AddCallerSkip(1))
	return &Logger{l}, nil
}

// 获取日志级别
func getZapLevel(level string) zapcore.Level {
	switch level {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

// 安全关闭
func (l *Logger) Sync() {
	l.Sync()
}

func (l *Logger) Errorf(format string, arg ...interface{}) {
	msg := fmt.Sprintf(format, arg...)
	l.Error(msg)
}

func (l *Logger) Infof(format string, arg ...interface{}) {
	msg := fmt.Sprintf(format, arg...)
	l.Info(msg)
}

func (l *Logger) Warnf(format string, arg ...interface{}) {
	msg := fmt.Sprintf(format, arg...)
	l.Warn(msg)
}

func (l *Logger) Panicf(format string, arg ...interface{}) {
	msg := fmt.Sprintf(format, arg...)
	l.Panic(msg)
}
