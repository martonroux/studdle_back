package billing

import (
	"context"
	"sync"
	"time"
)

// Prices is the pair of plan prices the pricing UI displays.
type Prices struct {
	Monthly PriceData
	Annual  PriceData
}

// PriceProvider returns the current pair of plan prices.
type PriceProvider interface {
	GetPrices(ctx context.Context) (Prices, error)
}

// NewCachedPriceProvider builds a PriceProvider that fetches both prices
// from c on first use and caches the result for ttl. Concurrent cold-cache
// callers may double-fetch; the window is small and singleflight is YAGNI.
func NewCachedPriceProvider(c Client, monthlyID, annualID string, ttl time.Duration) PriceProvider {
	return &cachedPriceProvider{
		client:    c,
		monthlyID: monthlyID,
		annualID:  annualID,
		ttl:       ttl,
	}
}

type cachedPriceProvider struct {
	client              Client
	monthlyID, annualID string
	ttl                 time.Duration

	mu      sync.Mutex
	cache   Prices
	fetched time.Time
	valid   bool
}

func (p *cachedPriceProvider) GetPrices(ctx context.Context) (Prices, error) {
	p.mu.Lock()
	if p.valid && time.Since(p.fetched) < p.ttl {
		out := p.cache
		p.mu.Unlock()
		return out, nil
	}
	p.mu.Unlock()

	monthly, err := p.client.GetPrice(ctx, p.monthlyID)
	if err != nil {
		return Prices{}, err
	}
	annual, err := p.client.GetPrice(ctx, p.annualID)
	if err != nil {
		return Prices{}, err
	}

	out := Prices{Monthly: monthly, Annual: annual}
	p.mu.Lock()
	p.cache = out
	p.fetched = time.Now()
	p.valid = true
	p.mu.Unlock()
	return out, nil
}
