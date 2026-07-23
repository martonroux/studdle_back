package aipipeline

// ModelMap resolves which provider model serves each AI feature.
type ModelMap struct {
	Default    string                // Default is the fallback model identifier
	PerFeature map[FeatureKey]string // PerFeature overrides the model for specific features
}

// For returns the model for feat, falling back to Default when no non-empty
// override exists.
func (m ModelMap) For(feat FeatureKey) string {
	if v := m.PerFeature[feat]; v != "" {
		return v
	}
	return m.Default
}
