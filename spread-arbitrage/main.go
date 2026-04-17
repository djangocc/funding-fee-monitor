package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"spread-arbitrage/internal/api"
	"spread-arbitrage/internal/clocksync"
	"spread-arbitrage/internal/engine"
	"spread-arbitrage/internal/exchange"
	"spread-arbitrage/internal/model"
	"spread-arbitrage/internal/wsmanager"
)

func main() {
	cfg := LoadConfig()

	// Create exchange clients
	clients := map[string]exchange.Client{
		"binance": exchange.NewBinanceClient(cfg.Binance.APIKey, cfg.Binance.APISecret, cfg.Binance.Unified),
		"aster":   exchange.NewAsterClient(cfg.Aster.User, cfg.Aster.Signer, cfg.Aster.PrivateKey),
		"okx":     exchange.NewOKXClient(cfg.OKX.APIKey, cfg.OKX.APISecret, cfg.OKX.Passphrase),
	}

	// Start clock sync (NTP-style, every 30s)
	clk := clocksync.New()
	clk.Start(30 * time.Second)

	// Create components
	hub := api.NewHub()
	taskMgr := engine.NewTaskManager()

	// Break circular dependency: WS Manager calls Engine.OnTick, Engine needs WS Manager
	var eng *engine.Engine
	wsMgr := wsmanager.New(clients,
		func(exchangeName, symbol string, tick model.BookTicker) {
			// Broadcast raw quote to frontend (for live price display independent of tasks)
			realAge := clk.RealAge(exchangeName, tick.Timestamp)
			hub.Broadcast(model.WSEvent{
				Type:   "quote",
				TaskID: "",
				Data: map[string]interface{}{
					"exchange":      exchangeName,
					"symbol":        tick.Symbol,
					"bid":           tick.Bid,
					"ask":           tick.Ask,
					"timestamp":     tick.Timestamp,
					"received_at":   tick.ReceivedAt,
					"real_age_ms":   realAge.Milliseconds(),
				},
			})
			if eng != nil {
				eng.OnTick(exchangeName, symbol, tick)
			}
		},
		func(exchangeName, symbol string, book model.OrderBook) {
			// Broadcast order book to frontend
			hub.Broadcast(model.WSEvent{
				Type:   "orderbook",
				TaskID: "",
				Data:   book,
			})
		},
	)
	// Convert clients map to map[string]interface{} for engine
	clientsInterface := make(map[string]interface{})
	for k, v := range clients {
		clientsInterface[k] = v
	}
	eng = engine.NewEngine(taskMgr, wsMgr, clientsInterface, clk, func(event model.WSEvent) {
		hub.Broadcast(event)
	})

	// Start background position sync (every 10 seconds)
	eng.StartPositionSync(10 * time.Second)

	// Start HTTP server
	server := api.NewServer(eng, taskMgr, clients, hub, wsMgr)
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
	clk.Stop()
	eng.StopPositionSync()
	wsMgr.Close()
	for _, c := range clients {
		c.Close()
	}
}
