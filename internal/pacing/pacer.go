package pacing

import "time"

// Pacer throttles streaming output to match real-time playback.
// BitratePacer uses configured bits/sec; future frame-aware pacers can implement the same interface.
type Pacer interface {
	Reset()
	WaitAfterWrite(n int)
}

// BitratePacer sleeps after each write so cumulative bytes sent match wall-clock time at the given bitrate.
type BitratePacer struct {
	bitrate int
	start   time.Time
	sent    int64
}

func NewBitratePacer(bitrate int) *BitratePacer {
	return &BitratePacer{bitrate: bitrate}
}

func (p *BitratePacer) Reset() {
	p.start = time.Now()
	p.sent = 0
}

func (p *BitratePacer) WaitAfterWrite(n int) {
	if p.bitrate <= 0 || n <= 0 {
		return
	}
	p.sent += int64(n)
	mediaMs := p.sent * 8 * 1000 / int64(p.bitrate)
	elapsedMs := time.Since(p.start).Milliseconds()
	if sleepMs := mediaMs - elapsedMs; sleepMs > 0 {
		time.Sleep(time.Duration(sleepMs) * time.Millisecond)
	}
}
