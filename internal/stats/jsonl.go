package stats

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// JSONLUsageLog appends one JSON line per turn to a file. Writes are
// serialised.
type JSONLUsageLog struct {
	mu sync.Mutex
	f  *os.File
}

var _ UsageSink = (*JSONLUsageLog)(nil)

// OpenJSONLUsageLog opens path for appending, creating it if needed.
func OpenJSONLUsageLog(path string) (*JSONLUsageLog, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("usage_log: %w", err)
	}
	return &JSONLUsageLog{f: f}, nil
}

// Write implements UsageSink. Writing after Close is a no-op.
func (l *JSONLUsageLog) Write(t Turn) error {
	line, err := json.Marshal(t)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	_, err = l.f.Write(append(line, '\n'))
	return err
}

// Close implements UsageSink; it may be called more than once.
func (l *JSONLUsageLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
