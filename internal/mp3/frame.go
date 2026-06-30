package mp3

import "time"

// FrameInfo describes a parsed MPEG audio frame header.
type FrameInfo struct {
	Version     int // 1, 2, or 25 (MPEG2.5)
	Layer       int // 1=Layer3, 2=Layer2, 3=Layer1
	Bitrate     int // bits per second
	SampleRate  int // Hz
	Channels    int // 1=mono, 2=stereo
	Samples     int // PCM samples in this frame
	FrameLength int // total frame size in bytes including header
	Duration    time.Duration
}

var (
	mpeg1Bitrates = []int{
		0, 32000, 40000, 48000, 56000, 64000, 80000, 96000,
		112000, 128000, 160000, 192000, 224000, 256000, 320000,
	}
	mpeg1SampleRates = []int{44100, 48000, 32000}

	mpeg2Bitrates = []int{
		0, 8000, 16000, 24000, 32000, 40000, 48000, 56000,
		64000, 80000, 96000, 112000, 128000, 144000, 160000,
	}
	mpeg2SampleRates = []int{22050, 24000, 16000}

	mpeg25SampleRates = []int{11025, 12000, 8000}
)

// IsSyncByte returns true if b is the first byte of an MPEG sync word.
func IsSyncByte(b byte) bool {
	return b == 0xFF
}

// IsValidHeader returns true if hdr looks like a valid MPEG audio frame header.
func IsValidHeader(hdr []byte) bool {
	_, err := ParseFrameHeader(hdr)
	return err == nil
}

// ParseFrameHeader parses a 4-byte MPEG audio frame header.
func ParseFrameHeader(hdr []byte) (FrameInfo, error) {
	if len(hdr) < 4 {
		return FrameInfo{}, errInvalidHeader
	}
	if hdr[0] != 0xFF || (hdr[1]&0xE0) != 0xE0 {
		return FrameInfo{}, errInvalidHeader
	}

	versionBits := (hdr[1] >> 3) & 0x03
	layerBits := (hdr[1] >> 1) & 0x03
	if layerBits == 0 || layerBits == 3 {
		return FrameInfo{}, errInvalidHeader
	}

	brIdx := int((hdr[2] >> 4) & 0x0F)
	srIdx := int((hdr[2] >> 2) & 0x03)
	if brIdx == 0x0F || srIdx == 0x03 {
		return FrameInfo{}, errInvalidHeader
	}

	padding := int((hdr[2] >> 1) & 0x01)
	channelMode := (hdr[3] >> 6) & 0x03

	var info FrameInfo
	info.Layer = 4 - int(layerBits) // 01->3, 10->2, 11->1

	switch versionBits {
	case 0x03:
		info.Version = 1
		if brIdx >= len(mpeg1Bitrates) || srIdx >= len(mpeg1SampleRates) {
			return FrameInfo{}, errInvalidHeader
		}
		info.Bitrate = mpeg1Bitrates[brIdx]
		info.SampleRate = mpeg1SampleRates[srIdx]
	case 0x02:
		info.Version = 2
		if brIdx >= len(mpeg2Bitrates) || srIdx >= len(mpeg2SampleRates) {
			return FrameInfo{}, errInvalidHeader
		}
		info.Bitrate = mpeg2Bitrates[brIdx]
		info.SampleRate = mpeg2SampleRates[srIdx]
	case 0x00:
		info.Version = 25
		if brIdx >= len(mpeg2Bitrates) || srIdx >= len(mpeg25SampleRates) {
			return FrameInfo{}, errInvalidHeader
		}
		info.Bitrate = mpeg2Bitrates[brIdx]
		info.SampleRate = mpeg25SampleRates[srIdx]
	default:
		return FrameInfo{}, errInvalidHeader
	}

	if info.Bitrate == 0 {
		return FrameInfo{}, errInvalidHeader
	}

	if channelMode == 3 {
		info.Channels = 1
	} else {
		info.Channels = 2
	}

	switch {
	case info.Layer == 3:
		if info.Version == 1 {
			info.Samples = 1152
			info.FrameLength = 144*info.Bitrate/info.SampleRate + padding
		} else {
			info.Samples = 576
			info.FrameLength = 72*info.Bitrate/info.SampleRate + padding
		}
	case info.Layer == 2:
		info.Samples = 1152
		if info.Version == 1 {
			info.FrameLength = 144*info.Bitrate/info.SampleRate + padding
		} else {
			info.FrameLength = 72*info.Bitrate/info.SampleRate + padding
		}
	case info.Layer == 1:
		info.Samples = 384
		info.FrameLength = (12*info.Bitrate/info.SampleRate+padding) * 4
	default:
		return FrameInfo{}, errInvalidHeader
	}

	if info.FrameLength < 4 {
		return FrameInfo{}, errInvalidHeader
	}

	info.Duration = time.Duration(info.Samples) * time.Second / time.Duration(info.SampleRate)
	return info, nil
}

var errInvalidHeader = errFrame("invalid MPEG audio frame header")

type errFrame string

func (e errFrame) Error() string { return string(e) }
