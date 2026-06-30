package mp3

import "io"

const (
	id3v2HeaderSize = 10
	id3v1TagSize    = 128
)

// id3v2Size reads the synchsafe size field at bytes 6-9 of an ID3v2 header.
func id3v2Size(header []byte) int {
	if len(header) < id3v2HeaderSize {
		return 0
	}
	return int(header[6]&0x7f)<<21 |
		int(header[7]&0x7f)<<14 |
		int(header[8]&0x7f)<<7 |
		int(header[9]&0x7f)
}

// isID3v1Trailer reports whether buf ends with an ID3v1 tag.
func isID3v1Trailer(buf []byte) bool {
	if len(buf) < id3v1TagSize {
		return false
	}
	start := len(buf) - id3v1TagSize
	return string(buf[start:start+3]) == "TAG"
}

// discardID3v2Leading reads from r and returns a reader positioned after a leading ID3v2 tag.
func discardID3v2Leading(r io.Reader) (io.Reader, error) {
	header := make([]byte, id3v2HeaderSize)
	n, err := io.ReadFull(r, header)
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return io.MultiReader(&byteSliceReader{b: header[:n]}, r), io.EOF
		}
		return nil, err
	}
	if string(header[0:3]) != "ID3" {
		return io.MultiReader(&byteSliceReader{b: header}, r), nil
	}
	size := id3v2Size(header)
	if size > 0 {
		if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
			return nil, err
		}
	}
	return r, nil
}

type byteSliceReader struct {
	b []byte
}

func (br *byteSliceReader) Read(p []byte) (int, error) {
	if len(br.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, br.b)
	br.b = br.b[n:]
	return n, nil
}
