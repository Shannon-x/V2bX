package hy2

import (
	"fmt"
	"net"
	"strings"

	"github.com/InazumaV/V2bX/common/format"
	"github.com/InazumaV/V2bX/limiter"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type serverLogger struct {
	Tag    string
	logger *zap.Logger
}

var logLevelMap = map[string]zapcore.Level{
	"debug": zapcore.DebugLevel,
	"info":  zapcore.InfoLevel,
	"warn":  zapcore.WarnLevel,
	"error": zapcore.ErrorLevel,
}

var logFormatMap = map[string]zapcore.EncoderConfig{
	"console": {
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		MessageKey:     "msg",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalColorLevelEncoder,
		EncodeTime:     zapcore.RFC3339TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
	},
	"json": {
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		MessageKey:     "msg",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.EpochMillisTimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
	},
}

func (l *serverLogger) Connect(addr net.Addr, uuid string, tx uint64) {
	limiterinfo, err := limiter.GetLimiter(l.Tag)
	if err != nil {
		// W1.3 / audit #10: never panic in the request path. The hysteria
		// stream goroutine has no recover, so a Panic here kills the entire
		// V2bX process. A missing limiter (e.g. during DeleteLimiter →
		// AddLimiter reload) is recoverable: log and skip the limit check.
		l.logger.Warn("Get limiter error, skipping limit check", zap.String("tag", l.Tag), zap.Error(err))
		l.logger.Info("client connected", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.Uint64("tx", tx))
		return
	}
	// W2.7 / W6.1 / audit #27 #3: atomic OverLimit; UserTag once; and a
	// 2s short-cache on the CheckLimit result so back-to-back callbacks
	// for the same flow don't re-run device-limit / sync.Map walks.
	tu := format.UserTag(l.Tag, uuid)
	_ = checkLimitCached(limiterinfo, tu, extractIPFromAddr(addr), addr.Network() == "tcp", true)
	l.logger.Info("client connected", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.Uint64("tx", tx))
}

func (l *serverLogger) Disconnect(addr net.Addr, uuid string, err error) {
	l.logger.Info("client disconnected", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.Error(err))
}

func (l *serverLogger) TCPRequest(addr net.Addr, uuid, reqAddr string) {
	limiterinfo, err := limiter.GetLimiter(l.Tag)
	if err != nil {
		// W1.3 / audit #10: log + skip; never panic in the request path.
		l.logger.Warn("Get limiter error, skipping limit check", zap.String("tag", l.Tag), zap.Error(err))
		l.logger.Debug("TCP request", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.String("reqAddr", reqAddr))
		return
	}
	// W2.7 / W6.1 / audit #27 #3: cached CheckLimit (2s window).
	tu := format.UserTag(l.Tag, uuid)
	_ = checkLimitCached(limiterinfo, tu, extractIPFromAddr(addr), addr.Network() == "tcp", true)
	l.logger.Debug("TCP request", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.String("reqAddr", reqAddr))
}

func (l *serverLogger) TCPError(addr net.Addr, uuid, reqAddr string, err error) {
	if err == nil {
		l.logger.Debug("TCP closed", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.String("reqAddr", reqAddr))
	} else {
		l.logger.Debug("TCP error", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.String("reqAddr", reqAddr), zap.Error(err))
	}
}

func (l *serverLogger) UDPRequest(addr net.Addr, uuid string, sessionId uint32, reqAddr string) {
	limiterinfo, err := limiter.GetLimiter(l.Tag)
	if err != nil {
		// W1.3 / audit #10: log + skip; never panic in the request path.
		l.logger.Warn("Get limiter error, skipping limit check", zap.String("tag", l.Tag), zap.Error(err))
		l.logger.Debug("UDP request", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.Uint32("sessionId", sessionId), zap.String("reqAddr", reqAddr))
		return
	}
	// W2.7 / W6.1 / audit #27 #3: cached CheckLimit (2s window).
	tu := format.UserTag(l.Tag, uuid)
	_ = checkLimitCached(limiterinfo, tu, extractIPFromAddr(addr), addr.Network() == "tcp", true)
	l.logger.Debug("UDP request", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.Uint32("sessionId", sessionId), zap.String("reqAddr", reqAddr))
}

func (l *serverLogger) UDPError(addr net.Addr, uuid string, sessionId uint32, err error) {
	if err == nil {
		l.logger.Debug("UDP closed", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.Uint32("sessionId", sessionId))
	} else {
		l.logger.Debug("UDP error", zap.String("addr", addr.String()), zap.String("uuid", uuid), zap.Uint32("sessionId", sessionId), zap.Error(err))
	}
}

func initLogger(logLevel string, logFormat string) (*zap.Logger, error) {
	level, ok := logLevelMap[strings.ToLower(logLevel)]
	if !ok {
		return nil, fmt.Errorf("unsupported log level: %s", logLevel)
	}
	enc, ok := logFormatMap[strings.ToLower(logFormat)]
	if !ok {
		return nil, fmt.Errorf("unsupported log format: %s", logFormat)
	}
	c := zap.Config{
		Level:             zap.NewAtomicLevelAt(level),
		DisableCaller:     true,
		DisableStacktrace: true,
		Encoding:          strings.ToLower(logFormat),
		EncoderConfig:     enc,
		OutputPaths:       []string{"stderr"},
		ErrorOutputPaths:  []string{"stderr"},
	}
	logger, err := c.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %s", err)
	}
	return logger, nil
}

func extractIPFromAddr(addr net.Addr) string {
	switch v := addr.(type) {
	case *net.TCPAddr:
		return v.IP.String()
	case *net.UDPAddr:
		return v.IP.String()
	case *net.IPAddr:
		return v.IP.String()
	default:
		return ""
	}
}
