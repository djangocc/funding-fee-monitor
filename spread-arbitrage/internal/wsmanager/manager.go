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
	mu            sync.RWMutex
	clients       map[string]exchange.Client
	latest        map[string]model.BookTicker   // key: "exchange:symbol"
	latestDepth   map[string]model.OrderBook    // key: "exchange:symbol"
	subs          map[string]context.CancelFunc // key: "exchange:symbol"
	depthSubs     map[string]context.CancelFunc // key: "exchange:symbol"
	onChange      func(exchangeName, symbol string, tick model.BookTicker)
	onDepthChange func(exchangeName, symbol string, book model.OrderBook)
}

func New(clients map[string]exchange.Client, onChange func(string, string, model.BookTicker), onDepthChange func(string, string, model.OrderBook)) *Manager {
	return &Manager{
		clients:       clients,
		latest:        make(map[string]model.BookTicker),
		latestDepth:   make(map[string]model.OrderBook),
		subs:          make(map[string]context.CancelFunc),
		depthSubs:     make(map[string]context.CancelFunc),
		onChange:      onChange,
		onDepthChange: onDepthChange,
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

// SubscribeDepth starts streaming OrderBook depth for a symbol on an exchange.
// Idempotent — if already subscribed, does nothing.
func (m *Manager) SubscribeDepth(ctx context.Context, exchangeName, symbol string) error {
	key := exchangeName + ":" + symbol
	m.mu.Lock()
	if _, exists := m.depthSubs[key]; exists {
		m.mu.Unlock()
		return nil // already subscribed
	}
	client, ok := m.clients[exchangeName]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown exchange: %s", exchangeName)
	}
	subCtx, cancel := context.WithCancel(ctx)
	m.depthSubs[key] = cancel
	m.mu.Unlock()

	ch, err := client.SubscribeDepth(subCtx, symbol)
	if err != nil {
		m.mu.Lock()
		delete(m.depthSubs, key)
		m.mu.Unlock()
		cancel()
		return err
	}

	go func() {
		for book := range ch {
			m.mu.Lock()
			m.latestDepth[key] = book
			m.mu.Unlock()
			if m.onDepthChange != nil {
				m.onDepthChange(exchangeName, symbol, book)
			}
		}
		log.Printf("[wsmanager] depth subscription closed: %s", key)
	}()

	return nil
}

// UnsubscribeDepth stops the depth stream for a symbol on an exchange.
func (m *Manager) UnsubscribeDepth(exchangeName, symbol string) {
	key := exchangeName + ":" + symbol
	m.mu.Lock()
	if cancel, ok := m.depthSubs[key]; ok {
		cancel()
		delete(m.depthSubs, key)
		delete(m.latestDepth, key)
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

// GetLatestDepth returns the most recent OrderBook for an exchange+symbol.
func (m *Manager) GetLatestDepth(exchangeName, symbol string) (model.OrderBook, bool) {
	key := exchangeName + ":" + symbol
	m.mu.RLock()
	book, ok := m.latestDepth[key]
	m.mu.RUnlock()
	return book, ok
}

// Close stops all subscriptions.
func (m *Manager) Close() {
	m.mu.Lock()
	for key, cancel := range m.subs {
		cancel()
		delete(m.subs, key)
	}
	for key, cancel := range m.depthSubs {
		cancel()
		delete(m.depthSubs, key)
	}
	m.latest = make(map[string]model.BookTicker)
	m.latestDepth = make(map[string]model.OrderBook)
	m.mu.Unlock()
}
