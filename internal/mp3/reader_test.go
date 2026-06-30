package mp3_test

import (
	"bytes"
	"testing"

	"github.com/darrenwiebe/icestream/internal/mp3"
)

func TestFrameReaderSkipsID3Prefix(t *testing.T) {
	frame := synthMPEG2Frame32k(t)
	id3 := []byte("ID3\x04\x00\x00\x00\x00\x00\x00\x04abcd")
	payload := append(id3, frame...)

	fr, err := mp3.NewFrameReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewFrameReader: %v", err)
	}

	first, _, err := fr.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if string(first[:2]) == "ID" {
		t.Fatal("first output still contains ID3 data")
	}
	if !mp3.IsSyncByte(first[0]) {
		t.Fatalf("first byte = %#x, want sync", first[0])
	}
}

func TestFrameReaderEmitsWholeFrames(t *testing.T) {
	frame := synthMPEG2Frame32k(t)
	id3 := []byte("ID3\x04\x00\x00\x00\x00\x00\x00\x04abcd")
	payload := append(id3, frame...)
	payload = append(payload, frame...)

	fr, err := mp3.NewFrameReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewFrameReader: %v", err)
	}

	first, _, err := fr.Next()
	if err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if !mp3.IsSyncByte(first[0]) {
		t.Fatalf("first frame does not start with sync")
	}
	if len(first) != len(frame) {
		t.Fatalf("first frame len = %d, want %d", len(first), len(frame))
	}

	second, _, err := fr.Next()
	if err != nil {
		t.Fatalf("second Next: %v", err)
	}
	if len(second) != len(frame) {
		t.Fatalf("second frame len = %d, want %d", len(second), len(frame))
	}
}
