package domain

import (
	"sync"

	"github.com/uwezukwechibuzor/termino/pkg/models"
)

// PriceAggregator maintains a sliding window of prices per symbol and computes aggregates.
type PriceAggregator struct {
	mu       sync.RWMutex
	windows  map[string]*PriceWindow
	maxSize  int
}

type PriceWindow struct {
	Prices    []models.PriceEvent
	LastPrice float64
	BasePrice float64 // price from ~24h ago for change calculation
}

func NewPriceAggregator(windowSize int) *PriceAggregator {
	return &PriceAggregator{
		windows: make(map[string]*PriceWindow),
		maxSize: windowSize,
	}
}

func (a *PriceAggregator) Add(event models.PriceEvent) models.AggregatedPrice {
	a.mu.Lock()
	defer a.mu.Unlock()

	w, ok := a.windows[event.Symbol]
	if !ok {
		w = &PriceWindow{BasePrice: event.Price}
		a.windows[event.Symbol] = w
	}

	w.Prices = append(w.Prices, event)
	if len(w.Prices) > a.maxSize {
		w.Prices = w.Prices[len(w.Prices)-a.maxSize:]
	}
	w.LastPrice = event.Price

	return a.computeAggregate(event.Symbol, w)
}

func (a *PriceAggregator) computeAggregate(symbol string, w *PriceWindow) models.AggregatedPrice {
	var sum, high, low, totalVol float64
	exchanges := make(map[string]struct{})

	high = w.Prices[0].Price
	low = w.Prices[0].Price

	for _, p := range w.Prices {
		sum += p.Price
		totalVol += p.Volume
		exchanges[p.Exchange] = struct{}{}
		if p.Price > high {
			high = p.Price
		}
		if p.Price < low {
			low = p.Price
		}
	}

	avg := sum / float64(len(w.Prices))
	change := w.LastPrice - w.BasePrice
	changePct := 0.0
	if w.BasePrice > 0 {
		changePct = (change / w.BasePrice) * 100
	}

	return models.AggregatedPrice{
		Symbol:        symbol,
		Price:         w.LastPrice,
		AvgPrice:      avg,
		HighPrice:     high,
		LowPrice:      low,
		Change24h:     change,
		ChangePct:     changePct,
		Volume:        totalVol,
		ExchangeCount: len(exchanges),
		Timestamp:     models.NowUnix(),
	}
}
