package wsmanager

import (
	"context"
	"fmt"
	"log"
	"sync"

	"spread-arbitrage/internal/exchange"
	"spread-arbitrage/internal/model"
)

type Manager struct {
	mu       sync.RWMutex
	clients  map[string]exchange.Client
	latest   map[string]model.BookTicker   // key: "exchange:symbol"
	subs     map[string]context.CancelFunc // key: "exchange:symbol"
	onChange func(exchangeName, symbol string, tick model.BookTicker)
}

func New(clients map[string]exchange.Client, onChange func(string, string, model.BookTicker)) *Manager {
	return &Manager{
		clients:  clients,
		latest:   make(map[string]model.BookTicker),
		subs:     make(map[string]context.CancelFunc),
		onChange: onChange,
	}
}

// Subscribe starts streaming BookTicker for a symbol on an exchange.
// Idempotent — if already subscribed, does nothing.
func (m *Manager) Subscribe(ctx context.Context, exchangeName, symbol string) error {
	key := exchangeName + ":" + symbol
	m.mu.Lock()
	if _, exists := m.subs[key]; exists {
		m.mu.Unlock()
		return nil // already subscribed
	}
	client, ok := m.clients[exchangeName]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown exchange: %s", exchangeName)
	}
	subCtx, cancel := context.WithCancel(ctx)
	m.subs[key] = cancel
	m.mu.Unlock()

	ch, err := client.SubscribeBookTicker(subCtx, symbol)
	if err != nil {
		m.mu.Lock()
		delete(m.subs, key)
		m.mu.Unlock()
		cancel()
		return err
	}

	go func() {
		for tick := range ch {
			m.mu.Lock()
			m.latest[key] = tick
			m.mu.Unlock()
			if m.onChange != nil {
				m.onChange(exchangeName, symbol, tick)
			}
		}
		log.Printf("[wsmanager] subscription closed: %s", key)
	}()

	return nil
}

// Unsubscribe stops the stream for a symbol on an exchange.
func (m *Manager) Unsubscribe(exchangeName, symbol string) {
	key := exchangeName + ":" + symbol
	m.mu.Lock()
	if cancel, ok := m.subs[key]; ok {
		cancel()
		delete(m.subs, key)
		delete(m.latest, key)
	}
	m.mu.Unlock()
}

// GetLatest returns the most recent BookTicker for an exchange+symbol.
func (m *Manager) GetLatest(exchangeName, symbol string) (model.BookTicker, bool) {
	key := exchangeName + ":" + symbol
	m.mu.RLock()
	tick, ok := m.latest[key]
	m.mu.RUnlock()
	return tick, ok
}

// Close stops all subscriptions.
func (m *Manager) Close() {
	m.mu.Lock()
	for key, cancel := range m.subs {
		cancel()
		delete(m.subs, key)
	}
	m.latest = make(map[string]model.BookTicker)
	m.mu.Unlock()
}
