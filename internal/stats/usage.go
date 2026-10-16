package stats

// UsageSink stores per-turn usage records. Records never contain prompt or
// response text.
type UsageSink interface {
	Write(t Turn) error
	Close() error
}
