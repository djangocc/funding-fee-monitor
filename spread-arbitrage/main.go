package main

import (
	"fmt"
	"log"
)

func main() {
	cfg := LoadConfig()
	log.Printf("Starting spread-arbitrage on port %s", cfg.Port)
	fmt.Println("Server starting...")
}
