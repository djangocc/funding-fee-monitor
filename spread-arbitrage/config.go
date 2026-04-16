package main

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type ExchangeConfig struct {
	APIKey     string
	APISecret  string
	Passphrase string // OKX only
}

type Config struct {
	Port    string
	Binance ExchangeConfig
	Aster   ExchangeConfig
	OKX     ExchangeConfig
}

func LoadConfig() Config {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "18527"
	}
	return Config{
		Port: port,
		Binance: ExchangeConfig{
			APIKey:    os.Getenv("BINANCE_API_KEY"),
			APISecret: os.Getenv("BINANCE_API_SECRET"),
		},
		Aster: ExchangeConfig{
			APIKey:    os.Getenv("ASTER_API_KEY"),
			APISecret: os.Getenv("ASTER_API_SECRET"),
		},
		OKX: ExchangeConfig{
			APIKey:    os.Getenv("OKX_API_KEY"),
			APISecret: os.Getenv("OKX_API_SECRET"),
			Passphrase: os.Getenv("OKX_PASSPHRASE"),
		},
	}
}
