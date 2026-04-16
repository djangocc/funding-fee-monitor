package api

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/gorilla/websocket"
	"spread-arbitrage/internal/model"
)

// wsConn wraps a websocket.Conn with a write mutex
type wsConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (wc *wsConn) WriteMessage(messageType int, data []byte) error {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	return wc.conn.WriteMessage(messageType, data)
}

func (wc *wsConn) Close() error {
	return wc.conn.Close()
}

type Hub struct {
	mu      sync.RWMutex
	clients map[*wsConn]bool
}

func NewHub() *Hub {
	return &Hub{clients: make(map[*wsConn]bool)}
}

func (h *Hub) Register(conn *websocket.Conn) *wsConn {
	wc := &wsConn{conn: conn}
	h.mu.Lock()
	h.clients[wc] = true
	h.mu.Unlock()
	return wc
}

func (h *Hub) Unregister(wc *wsConn) {
	h.mu.Lock()
	delete(h.clients, wc)
	h.mu.Unlock()
	wc.Close()
}

func (h *Hub) Broadcast(event model.WSEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("[ws] marshal error: %v", err)
		return
	}
	h.mu.RLock()
	clients := make([]*wsConn, 0, len(h.clients))
	for wc := range h.clients {
		clients = append(clients, wc)
	}
	h.mu.RUnlock()

	for _, wc := range clients {
		if err := wc.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("[ws] write error: %v", err)
			go h.Unregister(wc)
		}
	}
}
