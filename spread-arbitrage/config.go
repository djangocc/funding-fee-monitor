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
	Unified    bool   // Binance: use unified/portfolio margin account API
}

// AsterConfig holds Aster v3 specific credentials (EIP-712 wallet signing)
type AsterConfig struct {
	User       string // main wallet address
	Signer     string // API wallet address
	PrivateKey string // private key of signer wallet (hex, no 0x prefix)
}

type Config struct {
	Port    string
	Binance ExchangeConfig
	Aster   AsterConfig
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
			Unified:   os.Getenv("BINANCE_UNIFIED") == "true",
		},
		Aster: AsterConfig{
			User:       os.Getenv("ASTER_USER"),
			Signer:     os.Getenv("ASTER_SIGNER"),
			PrivateKey: os.Getenv("ASTER_PRIVATE_KEY"),
		},
		OKX: ExchangeConfig{
			APIKey:    os.Getenv("OKX_API_KEY"),
			APISecret: os.Getenv("OKX_API_SECRET"),
			Passphrase: os.Getenv("OKX_PASSPHRASE"),
		},
	}
}
