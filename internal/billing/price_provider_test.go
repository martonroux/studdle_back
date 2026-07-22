package billing_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"studdle/backend/internal/billing"
)

// fakeClient counts GetPrice calls and returns canned PriceData per ID.
type fakeClient struct {
	billing.NoopClient
	prices map[string]billing.PriceData
	err    error
	calls  int64
}

func (f *fakeClient) GetPrice(ctx context.Context, priceID string) (billing.PriceData, error) {
	atomic.AddInt64(&f.calls, 1)
	if f.err != nil {
		return billing.PriceData{}, f.err
	}
	p, ok := f.prices[priceID]
	if !ok {
		return billing.PriceData{}, errors.New("unknown price id")
	}
	return p, nil
}

func newFake() *fakeClient {
	return &fakeClient{
		prices: map[string]billing.PriceData{
			"price_monthly": {Amount: 699, Currency: "eur", Interval: "month"},
			"price_annual":  {Amount: 5999, Currency: "eur", Interval: "year"},
		},
	}
}

func TestCachedPriceProvider_FirstCallFetches(t *testing.T) {
	fc := newFake()
	p := billing.NewCachedPriceProvider(fc, "price_monthly", "price_annual", time.Minute)

	out, err := p.GetPrices(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.Monthly.Amount != 699 || out.Annual.Amount != 5999 {
		t.Fatalf("got %+v", out)
	}
	if got := atomic.LoadInt64(&fc.calls); got != 2 {
		t.Fatalf("calls = %d, want 2 (one per price)", got)
	}
}

func TestCachedPriceProvider_SecondCallHitsCache(t *testing.T) {
	fc := newFake()
	p := billing.NewCachedPriceProvider(fc, "price_monthly", "price_annual", time.Minute)

	if _, err := p.GetPrices(context.Background()); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := p.GetPrices(context.Background()); err != nil {
		t.Fatalf("second: %v", err)
	}
	if got := atomic.LoadInt64(&fc.calls); got != 2 {
		t.Fatalf("calls = %d, want 2 total (cache hit on second call)", got)
	}
}

func TestCachedPriceProvider_RefetchesAfterTTL(t *testing.T) {
	fc := newFake()
	p := billing.NewCachedPriceProvider(fc, "price_monthly", "price_annual", time.Nanosecond)

	if _, err := p.GetPrices(context.Background()); err != nil {
		t.Fatalf("first: %v", err)
	}
	time.Sleep(time.Millisecond) // ensure TTL elapsed
	if _, err := p.GetPrices(context.Background()); err != nil {
		t.Fatalf("second: %v", err)
	}
	if got := atomic.LoadInt64(&fc.calls); got != 4 {
		t.Fatalf("calls = %d, want 4 (two per fetch)", got)
	}
}

func TestCachedPriceProvider_ErrorDoesNotPoisonCache(t *testing.T) {
	fc := newFake()
	fc.err = errors.New("boom")
	p := billing.NewCachedPriceProvider(fc, "price_monthly", "price_annual", time.Minute)

	if _, err := p.GetPrices(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	fc.err = nil
	out, err := p.GetPrices(context.Background())
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if out.Monthly.Amount != 699 {
		t.Fatalf("got %+v", out)
	}
}

func TestCachedPriceProvider_ConcurrentSafe(t *testing.T) {
	fc := newFake()
	p := billing.NewCachedPriceProvider(fc, "price_monthly", "price_annual", time.Minute)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			out, err := p.GetPrices(context.Background())
			if err != nil {
				t.Errorf("goroutine err: %v", err)
				return
			}
			if out.Monthly.Amount != 699 || out.Annual.Amount != 5999 {
				t.Errorf("goroutine got %+v", out)
			}
		}()
	}
	wg.Wait()
}
