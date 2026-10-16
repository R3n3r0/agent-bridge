package stats

// DiscardUsage drops every record.
type DiscardUsage struct{}

var _ UsageSink = DiscardUsage{}

// Write implements UsageSink.
func (DiscardUsage) Write(Turn) error { return nil }

// Close implements UsageSink.
func (DiscardUsage) Close() error { return nil }
