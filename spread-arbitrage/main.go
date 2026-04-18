package main

import (
	"context"
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

	clients := map[string]exchange.Client{
		"binance": exchange.NewBinanceClient(cfg.Binance.APIKey, cfg.Binance.APISecret, cfg.Binance.Unified),
		"aster":   exchange.NewAsterClient(cfg.Aster.User, cfg.Aster.Signer, cfg.Aster.PrivateKey),
		"okx":     exchange.NewOKXClient(cfg.OKX.APIKey, cfg.OKX.APISecret, cfg.OKX.Passphrase),
	}

	clk := clocksync.New()
	clk.Start(30 * time.Second)

	hub := api.NewHub()
	taskMgr := engine.NewTaskManager()

	var eng *engine.Engine
	wsMgr := wsmanager.New(clients,
		func(exchangeName, symbol string, tick model.BookTicker) {
			// Engine only — no frontend broadcast
			if eng != nil {
				eng.OnTick(exchangeName, symbol, tick)
			}
		},
		func(exchangeName, symbol string, book model.OrderBook) {
			// Engine only — frontend gets orderbook directly from exchange
			_ = book
		},
	)

	clientsInterface := make(map[string]interface{})
	for k, v := range clients {
		clientsInterface[k] = v
	}
	eng = engine.NewEngine(taskMgr, wsMgr, clientsInterface, clk, func(event model.WSEvent) {
		hub.Broadcast(event)
	})

	// Subscribe to User Data Streams for real-time order/position updates
	ctx := context.Background()
	for name, client := range clients {
		exchangeName := name
		err := client.SubscribeUserData(ctx, exchange.UserDataCallbacks{
			OnOrderUpdate: func(u model.OrderUpdate) {
				eng.OnOrderUpdate(u)
				hub.Broadcast(model.WSEvent{Type: "order_update", Data: u})
			},
			OnAccountUpdate: func(u model.AccountUpdate) {
				eng.OnAccountUpdate(u)
				hub.Broadcast(model.WSEvent{Type: "position_update", Data: u})
			},
		})
		if err != nil {
			log.Printf("[main] Warning: failed to subscribe user data for %s: %v", exchangeName, err)
		} else {
			log.Printf("[main] Subscribed to user data stream: %s", exchangeName)
		}
	}

	// Warm up HTTP connection pools (TCP+TLS handshake) so first trade has no cold start
	go func() {
		for name, client := range clients {
			if _, err := client.GetFundingRate(context.Background(), "BTCUSDT"); err != nil {
				log.Printf("[main] Warmup %s: %v (non-fatal)", name, err)
			} else {
				log.Printf("[main] Warmup %s: connection pool ready", name)
			}
		}
	}()

	// Start background jobs: position sync (5s), inflight timeout (2s), balance checker (2s)
	eng.StartBackgroundJobs()

	server := api.NewServer(eng, taskMgr, clients, hub, wsMgr)
	go func() {
		log.Printf("Server listening on :%s", cfg.Port)
		if err := server.Run(":" + cfg.Port); err != nil {
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down...")
	clk.Stop()
	eng.StopBackgroundJobs()
	wsMgr.Close()
	for _, c := range clients {
		c.Close()
	}
}
