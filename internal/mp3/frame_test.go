package mp3_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/darrenwiebe/icestream/internal/mp3"
)

func TestParseFrameHeaderRejectsGarbage(t *testing.T) {
	_, err := mp3.ParseFrameHeader([]byte{0x00, 0x00, 0x00, 0x00})
	if err == nil {
		t.Fatal("expected error for garbage header")
	}
}

func TestIsValidHeader(t *testing.T) {
	hdr := []byte{0xFF, 0xF3, 0x40, 0xC0}
	if !mp3.IsValidHeader(hdr) {
		t.Fatal("expected valid MPEG2 32k header")
	}
}

func TestParseFrameHeaderMPEG2Mono32k(t *testing.T) {
	frame := synthMPEG2Frame32k(t)
	info, err := mp3.ParseFrameHeader(frame[:4])
	if err != nil {
		t.Fatalf("ParseFrameHeader: %v", err)
	}
	if info.Bitrate != 32000 {
		t.Fatalf("bitrate = %d, want 32000", info.Bitrate)
	}
	if info.SampleRate != 22050 {
		t.Fatalf("sample rate = %d, want 22050", info.SampleRate)
	}
	if info.FrameLength != len(frame) {
		t.Fatalf("frame length = %d, want %d", info.FrameLength, len(frame))
	}
}

func TestSingleFrameFileEOF(t *testing.T) {
	frame := synthMPEG2Frame32k(t)
	fr, err := mp3.NewFrameReader(bytes.NewReader(frame))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for {
		_, _, err := fr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != 1 {
		t.Fatalf("frame count = %d, want 1", count)
	}
}

func synthMPEG2Frame32k(t *testing.T) []byte {
	t.Helper()
	hdr := []byte{0xFF, 0xF3, 0x40, 0xC0}
	info, err := mp3.ParseFrameHeader(hdr)
	if err != nil {
		t.Fatalf("parse synth header: %v", err)
	}
	frame := make([]byte, info.FrameLength)
	copy(frame, hdr)
	return frame
}
