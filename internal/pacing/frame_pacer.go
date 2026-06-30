package pacing

import "time"

// FramePacer sleeps after each MP3 frame so cumulative playback time matches wall clock.
type FramePacer struct {
	start  time.Time
	played time.Duration
}

func NewFramePacer() *FramePacer {
	return &FramePacer{}
}

func (p *FramePacer) Reset() {
	p.start = time.Now()
	p.played = 0
}

// WaitAfterFrame blocks until wall clock catches up with the frame's decoded duration.
func (p *FramePacer) WaitAfterFrame(duration time.Duration) {
	if duration <= 0 {
		return
	}
	p.played += duration
	elapsed := time.Since(p.start)
	if sleep := p.played - elapsed; sleep > 0 {
		time.Sleep(sleep)
	}
}

// WaitAfterWrite satisfies Pacer for fallback paths; prefer WaitAfterFrame for MP3.
func (p *FramePacer) WaitAfterWrite(n int) {}
