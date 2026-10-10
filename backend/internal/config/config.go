package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	AppEnv       string
	HTTPAddr     string
	DatabaseURL  string
	RedisAddr    string
	KafkaBrokers []string
	KafkaGroupID string
	JWTSecret    string
}

func Load() (Config, error) {
	configFile := ".env"
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		configFile = filepath.Join("backend", ".env")
	}
	viper.SetConfigFile(configFile)
	viper.SetConfigType("env")
	_ = viper.ReadInConfig()
	viper.AutomaticEnv()
	if viper.GetString("HTTP_ADDR") == "" {
		if port := viper.GetString("PORT"); port != "" {
			viper.Set("HTTP_ADDR", ":"+port)
		}
	}
	viper.SetDefault("APP_ENV", "development")
	viper.SetDefault("HTTP_ADDR", ":8080")
	viper.SetDefault("REDIS_ADDR", "localhost:6379")
	viper.SetDefault("KAFKA_GROUP_ID", "brainbird-worker")

	brokers := strings.Split(viper.GetString("KAFKA_BROKERS"), ",")
	cfg := Config{
		AppEnv: viper.GetString("APP_ENV"), HTTPAddr: viper.GetString("HTTP_ADDR"),
		DatabaseURL: viper.GetString("DATABASE_URL"), RedisAddr: viper.GetString("REDIS_ADDR"),
		KafkaBrokers: brokers, KafkaGroupID: viper.GetString("KAFKA_GROUP_ID"), JWTSecret: viper.GetString("JWT_SECRET"),
	}
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = "local-development-secret"
	}
	return cfg, nil
}
