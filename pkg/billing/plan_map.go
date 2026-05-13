package billing

// PriceMap is the two-way lookup between local Plan names and Stripe price IDs.
// Constructed from config at boot.
type PriceMap struct {
	Monthly string // Monthly is the Stripe price id for pro_monthly
	Annual  string // Annual is the Stripe price id for pro_annual
}

// PlanFromPriceID returns the local Plan that corresponds to priceID, or false.
func (m PriceMap) PlanFromPriceID(priceID string) (Plan, bool) {
	switch priceID {
	case m.Monthly:
		return PlanProMonthly, true
	case m.Annual:
		return PlanProAnnual, true
	default:
		return "", false
	}
}

// PriceIDFromPlan returns the Stripe price id for plan, or false for PlanComp.
func (m PriceMap) PriceIDFromPlan(plan Plan) (string, bool) {
	switch plan {
	case PlanProMonthly:
		return m.Monthly, true
	case PlanProAnnual:
		return m.Annual, true
	default:
		return "", false
	}
}
