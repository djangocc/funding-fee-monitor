package exchange

import (
	"context"
	"spread-arbitrage/internal/model"
)

type Client interface {
	Name() string
	SubscribeBookTicker(ctx context.Context, symbol string) (<-chan model.BookTicker, error)
	SubscribeDepth(ctx context.Context, symbol string) (<-chan model.OrderBook, error)
	PlaceMarketOrder(ctx context.Context, symbol string, side string, quantity float64, clientOrderID string) (*model.Order, error)
	GetPosition(ctx context.Context, symbol string) (*model.Position, error)
	GetOrders(ctx context.Context, symbol string) ([]model.Order, error)
	GetFundingRate(ctx context.Context, symbol string) (*model.FundingRate, error)
	Close() error
}
