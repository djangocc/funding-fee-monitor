package exchange

import (
	"context"
	"spread-arbitrage/internal/model"
)

type Client interface {
	Name() string
	SubscribeBookTicker(ctx context.Context, symbol string) (<-chan model.BookTicker, error)
	SubscribeDepth(ctx context.Context, symbol string) (<-chan model.OrderBook, error)
	PlaceMarketOrder(ctx context.Context, symbol string, side string, quantity float64) (*model.Order, error)
	GetPosition(ctx context.Context, symbol string) (*model.Position, error)
	GetTrades(ctx context.Context, symbol string) ([]model.Trade, error)
	Close() error
}
