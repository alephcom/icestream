package pacing_test

import (
	"testing"
	"time"

	"github.com/darrenwiebe/icestream/internal/pacing"
)

func TestBitratePacerTiming(t *testing.T) {
	const bitrate = 128000
	const totalBytes = 128000 / 8 // 1 second of audio at 128 kbps

	p := pacing.NewBitratePacer(bitrate)
	p.Reset()

	start := time.Now()
	chunk := 4096
	for sent := 0; sent < totalBytes; sent += chunk {
		n := chunk
		if sent+n > totalBytes {
			n = totalBytes - sent
		}
		p.WaitAfterWrite(n)
	}
	elapsed := time.Since(start)

	want := time.Duration(totalBytes*8*1000/bitrate) * time.Millisecond
	tolerance := 50 * time.Millisecond
	if elapsed < want-tolerance || elapsed > want+tolerance {
		t.Fatalf("elapsed = %v, want ~%v (±%v)", elapsed, want, tolerance)
	}
}

func TestBitratePacerZeroBitrate(t *testing.T) {
	p := pacing.NewBitratePacer(0)
	p.Reset()
	start := time.Now()
	p.WaitAfterWrite(10000)
	if time.Since(start) > 10*time.Millisecond {
		t.Fatal("zero bitrate should not sleep")
	}
}
