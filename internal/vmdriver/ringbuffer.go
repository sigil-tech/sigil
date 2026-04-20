package vmdriver

import "sync"

// ringBuffer is a bounded byte buffer for capturing hypervisor subprocess
// stderr. When full, older bytes are discarded so the last cap bytes are
// retained — enough to include the most recent diagnostic context in a
// failure message without unbounded memory use.
type ringBuffer struct {
	mu   sync.Mutex
	data []byte
	cap  int
}

func newRingBuffer(cap int) *ringBuffer {
	return &ringBuffer{cap: cap, data: make([]byte, 0, min(cap, 64*1024))}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data = append(r.data, p...)
	if len(r.data) > r.cap {
		r.data = r.data[len(r.data)-r.cap:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.data)
}

// drainTo reads from src until EOF, writing each chunk to dst. Intended to
// be launched as a goroutine against a subprocess stderr pipe.
func drainTo(src interface{ Read([]byte) (int, error) }, dst *ringBuffer) {
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			dst.Write(buf[:n]) //nolint:errcheck
		}
		if err != nil {
			return
		}
	}
}
