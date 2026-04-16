package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"spread-arbitrage/internal/api"
	"spread-arbitrage/internal/engine"
	"spread-arbitrage/internal/exchange"
	"spread-arbitrage/internal/model"
	"spread-arbitrage/internal/wsmanager"
)

func main() {
	cfg := LoadConfig()

	// Create exchange clients
	clients := map[string]exchange.Client{
		"binance": exchange.NewBinanceClient(cfg.Binance.APIKey, cfg.Binance.APISecret),
		"aster":   exchange.NewAsterClient(cfg.Aster.APIKey, cfg.Aster.APISecret),
		"okx":     exchange.NewOKXClient(cfg.OKX.APIKey, cfg.OKX.APISecret, cfg.OKX.Passphrase),
	}

	// Create components
	hub := api.NewHub()
	taskMgr := engine.NewTaskManager()

	// Break circular dependency: WS Manager calls Engine.OnTick, Engine needs WS Manager
	var eng *engine.Engine
	wsMgr := wsmanager.New(clients, func(exchangeName, symbol string, tick model.BookTicker) {
		if eng != nil {
			eng.OnTick(exchangeName, symbol, tick)
		}
	})
	// Convert clients map to map[string]interface{} for engine
	clientsInterface := make(map[string]interface{})
	for k, v := range clients {
		clientsInterface[k] = v
	}
	eng = engine.NewEngine(taskMgr, wsMgr, clientsInterface, func(event model.WSEvent) {
		hub.Broadcast(event)
	})

	// Start HTTP server
	server := api.NewServer(eng, taskMgr, clients, hub)
	go func() {
		log.Printf("Server listening on :%s", cfg.Port)
		if err := server.Run(":" + cfg.Port); err != nil {
			log.Fatal(err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down...")
	wsMgr.Close()
	for _, c := range clients {
		c.Close()
	}
}
