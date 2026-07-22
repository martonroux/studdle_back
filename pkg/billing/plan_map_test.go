package billing_test

import (
	"testing"

	pkgbilling "studdle/backend/pkg/billing"
)

func TestPlanFromPriceID(t *testing.T) {
	m := pkgbilling.PriceMap{
		Monthly: "price_M1",
		Annual:  "price_A1",
	}
	if p, ok := m.PlanFromPriceID("price_M1"); !ok || p != pkgbilling.PlanProMonthly {
		t.Fatalf("monthly mismatch: %v ok=%v", p, ok)
	}
	if p, ok := m.PlanFromPriceID("price_A1"); !ok || p != pkgbilling.PlanProAnnual {
		t.Fatalf("annual mismatch: %v ok=%v", p, ok)
	}
	if _, ok := m.PlanFromPriceID("price_unknown"); ok {
		t.Fatalf("unknown price should return ok=false")
	}
}

func TestPriceIDFromPlan(t *testing.T) {
	m := pkgbilling.PriceMap{Monthly: "price_M1", Annual: "price_A1"}
	if got, _ := m.PriceIDFromPlan(pkgbilling.PlanProMonthly); got != "price_M1" {
		t.Fatalf("got %q", got)
	}
	if got, _ := m.PriceIDFromPlan(pkgbilling.PlanProAnnual); got != "price_A1" {
		t.Fatalf("got %q", got)
	}
	if _, ok := m.PriceIDFromPlan(pkgbilling.PlanComp); ok {
		t.Fatalf("comp plan has no price id")
	}
}
