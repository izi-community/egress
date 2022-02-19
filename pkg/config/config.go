package config

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/zapr"
	"github.com/livekit/protocol/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"
)

type Config struct {
	HealthPort   int         `yaml:"health_port"`
	LogLevel     string      `yaml:"log_level"`
	TemplateBase string      `yaml:"template_base"`
	Insecure     bool        `yaml:"insecure"`
	Redis        RedisConfig `yaml:"redis"`
	Display      string      `yaml:"-"`
}

type RedisConfig struct {
	Address  string `yaml:"address"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

func NewConfig(confString string) (*Config, error) {
	conf := &Config{
		LogLevel:     "info",
		TemplateBase: "https://recorder.livekit.io/#",
	}

	if confString != "" {
		if err := yaml.Unmarshal([]byte(confString), conf); err != nil {
			return nil, fmt.Errorf("could not parse config: %v", err)
		}
	}

	// GStreamer log level
	if os.Getenv("GST_DEBUG") == "" {
		var gstDebug int
		switch conf.LogLevel {
		case "debug":
			gstDebug = 2
		case "info", "warn", "error":
			gstDebug = 1
		case "panic":
			gstDebug = 0
		}
		if err := os.Setenv("GST_DEBUG", fmt.Sprint(gstDebug)); err != nil {
			return nil, err
		}
	}

	conf.initLogger()
	err := conf.initDisplay()
	return conf, err
}

func (c *Config) initDisplay() error {
	d := os.Getenv("DISPLAY")
	if d != "" && strings.HasPrefix(d, ":") {
		num, err := strconv.Atoi(d[1:])
		if err == nil && num > 0 && num <= 2147483647 {
			c.Display = d
			return nil
		}
	}

	if c.Display == "" {
		rand.Seed(time.Now().UnixNano())
		c.Display = fmt.Sprintf(":%d", 10+rand.Intn(2147483637))
	}

	// GStreamer uses display from env
	if err := os.Setenv("DISPLAY", c.Display); err != nil {
		return err
	}

	return nil
}

func (c *Config) initLogger() {
	conf := zap.NewProductionConfig()
	if c.LogLevel != "" {
		lvl := zapcore.Level(0)
		if err := lvl.UnmarshalText([]byte(c.LogLevel)); err == nil {
			conf.Level = zap.NewAtomicLevelAt(lvl)
		}
	}

	l, _ := conf.Build()
	logger.SetLogger(zapr.NewLogger(l), "livekit-egress")
}
