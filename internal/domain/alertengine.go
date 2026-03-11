package domain

import (
	"fmt"
	"sync"

	"github.com/uwezukwechibuzor/termino/pkg/models"
)

// AlertEngine evaluates price data against configured alert rules.
type AlertEngine struct {
	mu    sync.RWMutex
	rules []models.AlertRule
	fired map[string]bool // track fired alerts to avoid duplicates
}

func NewAlertEngine(rules []models.AlertRule) *AlertEngine {
	return &AlertEngine{
		rules: rules,
		fired: make(map[string]bool),
	}
}

func (e *AlertEngine) AddRule(rule models.AlertRule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = append(e.rules, rule)
}

// RemoveRule removes a rule by ID.
func (e *AlertEngine) RemoveRule(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	for i, r := range e.rules {
		if r.ID == id {
			e.rules = append(e.rules[:i], e.rules[i+1:]...)
			key := fmt.Sprintf("%s-%s-%.2f", r.Symbol, r.Direction, r.Threshold)
			delete(e.fired, key)
			return true
		}
	}
	return false
}

// ListRules returns a copy of all current rules.
func (e *AlertEngine) ListRules() []models.AlertRule {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]models.AlertRule, len(e.rules))
	copy(result, e.rules)
	return result
}

func (e *AlertEngine) Evaluate(price models.AggregatedPrice) []models.PriceAlert {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var alerts []models.PriceAlert

	for _, rule := range e.rules {
		if rule.Symbol != price.Symbol {
			continue
		}

		key := fmt.Sprintf("%s-%s-%.2f", rule.Symbol, rule.Direction, rule.Threshold)
		triggered := false

		switch rule.Direction {
		case "above":
			triggered = price.Price >= rule.Threshold
		case "below":
			triggered = price.Price <= rule.Threshold
		}

		if triggered && !e.fired[key] {
			e.fired[key] = true
			alerts = append(alerts, models.PriceAlert{
				ID:        fmt.Sprintf("alert-%s-%d", rule.Symbol, price.Timestamp),
				Symbol:    rule.Symbol,
				Rule:      fmt.Sprintf("%s %s %.2f", rule.Symbol, rule.Direction, rule.Threshold),
				Threshold: rule.Threshold,
				Current:   price.Price,
				Direction: rule.Direction,
				Message:   fmt.Sprintf("%s price is %.2f (%s threshold %.2f)", rule.Symbol, price.Price, rule.Direction, rule.Threshold),
				Timestamp: price.Timestamp,
			})
		} else if !triggered {
			// Reset the fired flag so it can trigger again
			delete(e.fired, key)
		}
	}

	return alerts
}
