package llm

import "sync"

// Price is USD per 1M tokens.
type Price struct{ InputPerM, OutputPerM, CacheReadPerM float64 }

var (
	priceMu sync.RWMutex
	// Anthropic list prices as of 2026-10. Gemini models are deliberately
	// unlisted until measured; use SetPrice (--price) to add them.
	prices = map[string]Price{
		"claude-haiku-5-5":  {0.10, 0.50, 0.01},
		"claude-sonnet-5-5": {2, 10, 0.20},
		"claude-opus-5-5":   {4, 20, 0.20},
	}
)

// Cost returns USD for u under the price table; known=false (cost 0) for an unlisted model.
func Cost(model string, u Usage) (usd float64, known bool) {
	priceMu.RLock()
	p, ok := prices[model]
	priceMu.RUnlock()
	if !ok {
		return 0, false
	}
	usd = (float64(u.InputTokens)*p.InputPerM + float64(u.OutputTokens)*p.OutputPerM + float64(u.CacheReadTokens)*p.CacheReadPerM) / 1e6
	return usd, true
}

// SetPrice overrides or adds a table entry.
func SetPrice(model string, p Price) {
	priceMu.Lock()
	prices[model] = p
	priceMu.Unlock()
}
