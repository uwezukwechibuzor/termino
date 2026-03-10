package models

import (
	"encoding/json"
	"time"
)

// PriceEvent represents a raw price event from an exchange.
type PriceEvent struct {
	Symbol    string  `json:"symbol"`
	Price     float64 `json:"price"`
	Exchange  string  `json:"exchange"`
	Volume    float64 `json:"volume,omitempty"`
	Timestamp int64   `json:"timestamp"`
}

// AggregatedPrice represents a processed price with analytics.
type AggregatedPrice struct {
	Symbol       string  `json:"symbol"`
	Price        float64 `json:"price"`
	AvgPrice     float64 `json:"avg_price"`
	HighPrice    float64 `json:"high_price"`
	LowPrice     float64 `json:"low_price"`
	Change24h    float64 `json:"change_24h"`
	ChangePct    float64 `json:"change_pct"`
	Volume       float64 `json:"volume"`
	ExchangeCount int   `json:"exchange_count"`
	Timestamp    int64   `json:"timestamp"`
}

// PriceAlert represents a triggered price alert.
type PriceAlert struct {
	ID        string  `json:"id"`
	Symbol    string  `json:"symbol"`
	Rule      string  `json:"rule"`
	Threshold float64 `json:"threshold"`
	Current   float64 `json:"current"`
	Direction string  `json:"direction"` // "above" or "below"
	Message   string  `json:"message"`
	Timestamp int64   `json:"timestamp"`
}

// AlertRule defines a price alert rule.
type AlertRule struct {
	Symbol    string  `json:"symbol"`
	Threshold float64 `json:"threshold"`
	Direction string  `json:"direction"` // "above" or "below"
}

func (p *PriceEvent) Marshal() ([]byte, error) {
	return json.Marshal(p)
}

func UnmarshalPriceEvent(data []byte) (*PriceEvent, error) {
	var e PriceEvent
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

func (a *AggregatedPrice) Marshal() ([]byte, error) {
	return json.Marshal(a)
}

func UnmarshalAggregatedPrice(data []byte) (*AggregatedPrice, error) {
	var a AggregatedPrice
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func (al *PriceAlert) Marshal() ([]byte, error) {
	return json.Marshal(al)
}

func UnmarshalPriceAlert(data []byte) (*PriceAlert, error) {
	var a PriceAlert
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func NowUnix() int64 {
	return time.Now().Unix()
}
