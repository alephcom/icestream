package mp3

import (
	"bytes"
	"io"
)

const maxFrameSize = 8192

// FrameReader reads MP3 elementary stream frames from r, skipping ID3v2 tags
// and emitting only complete frames suitable for live Icecast streaming.
type FrameReader struct {
	r   io.Reader
	buf []byte
	eof bool
}

// NewFrameReader returns a reader that skips a leading ID3v2 tag and yields frames.
func NewFrameReader(r io.Reader) (*FrameReader, error) {
	fr := &FrameReader{r: r}
	if err := fr.init(); err != nil && err != io.EOF {
		return nil, err
	}
	return fr, nil
}

func (fr *FrameReader) init() error {
	afterID3, err := discardID3v2Leading(fr.r)
	if err != nil && err != io.EOF {
		return err
	}
	fr.r = afterID3
	return fr.discardToSync()
}

func (fr *FrameReader) discardToSync() error {
	for {
		if fr.eof && len(fr.buf) == 0 {
			return io.EOF
		}
		idx := fr.findSync(0)
		if idx < 0 {
			if fr.eof {
				fr.buf = nil
				return io.EOF
			}
			if err := fr.fill(); err != nil {
				return err
			}
			continue
		}
		if idx > 0 {
			fr.buf = fr.buf[idx:]
		}
		return nil
	}
}

func (fr *FrameReader) findSync(start int) int {
	for i := start; i < len(fr.buf); i++ {
		if fr.buf[i] != 0xFF {
			continue
		}
		if i+1 >= len(fr.buf) {
			return i
		}
		if (fr.buf[i+1] & 0xE0) != 0xE0 {
			continue
		}
		if i+4 <= len(fr.buf) && IsValidHeader(fr.buf[i:i+4]) {
			return i
		}
		if i+4 > len(fr.buf) {
			return i
		}
	}
	return -1
}

func (fr *FrameReader) fill() error {
	if fr.eof {
		return io.EOF
	}
	chunk := make([]byte, 4096)
	n, err := fr.r.Read(chunk)
	if n > 0 {
		fr.buf = append(fr.buf, chunk[:n]...)
	}
	if err == io.EOF {
		fr.eof = true
		if n == 0 {
			return io.EOF
		}
		return nil
	}
	return err
}

// Next returns the next complete MP3 frame and its header info.
func (fr *FrameReader) Next() ([]byte, FrameInfo, error) {
	for {
		if err := fr.discardToSync(); err != nil {
			return nil, FrameInfo{}, err
		}

		if len(fr.buf) < 4 {
			if err := fr.fill(); err != nil {
				if err == io.EOF {
					fr.buf = nil
					return nil, FrameInfo{}, io.EOF
				}
				return nil, FrameInfo{}, err
			}
			continue
		}

		info, err := ParseFrameHeader(fr.buf[:4])
		if err != nil {
			fr.buf = fr.buf[1:]
			continue
		}

		if info.FrameLength > maxFrameSize {
			fr.buf = fr.buf[1:]
			continue
		}

		for len(fr.buf) < info.FrameLength {
			if err := fr.fill(); err != nil {
				if err == io.EOF {
					// Drop incomplete trailing frame.
					fr.buf = nil
					return nil, FrameInfo{}, io.EOF
				}
				return nil, FrameInfo{}, err
			}
		}

		frame := bytes.Clone(fr.buf[:info.FrameLength])
		fr.buf = fr.buf[info.FrameLength:]

		// Strip ID3v1 if this was the last frame of the file.
		if fr.eof && len(fr.buf) == 0 && isID3v1Trailer(frame) {
			frame = frame[:len(frame)-id3v1TagSize]
			if len(frame) < 4 {
				return nil, FrameInfo{}, io.EOF
			}
			info, err = ParseFrameHeader(frame[:4])
			if err != nil {
				return nil, FrameInfo{}, io.EOF
			}
		}

		return frame, info, nil
	}
}
