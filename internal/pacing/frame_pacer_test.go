package pacing_test

import (
	"testing"
	"time"

	"github.com/darrenwiebe/icestream/internal/pacing"
)

func TestFramePacerTiming(t *testing.T) {
	p := pacing.NewFramePacer()
	p.Reset()

	d := 26 * time.Millisecond
	start := time.Now()
	for i := 0; i < 10; i++ {
		p.WaitAfterFrame(d)
	}
	elapsed := time.Since(start)

	want := 10 * d
	tolerance := 40 * time.Millisecond
	if elapsed < want-tolerance || elapsed > want+tolerance {
		t.Fatalf("elapsed = %v, want ~%v (±%v)", elapsed, want, tolerance)
	}
}
