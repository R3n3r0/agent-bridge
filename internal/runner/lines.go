package runner

import (
	"bytes"
	"sync"
)

// one. Lines longer than max are dropped.
type lineWriter struct {
	max      int
	fn       func([]byte)
	buf      []byte
	overflow bool
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.append(p)
			break
		}
		w.append(p[:i])
		w.emit()
		p = p[i+1:]
	}
	return n, nil
}

func (w *lineWriter) append(p []byte) {
	if w.overflow {
		return
	}
	if len(w.buf)+len(p) > w.max {
		w.overflow = true
		w.buf = nil
		return
	}
	w.buf = append(w.buf, p...)
}

func (w *lineWriter) emit() {
	line := bytes.TrimSpace(w.buf)
	if w.overflow {
		w.fn([]byte("\x00overflow"))
	} else if len(line) > 0 {
		w.fn(line)
	}
	w.buf = w.buf[:0]
	w.overflow = false
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 || w.overflow {
		w.emit()
	}
}

// tailBuffer keeps only the last max bytes written.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.max:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
