package metadata

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dhowden/tag"
)

func TitleForPath(path string) string {
	if title := readTagTitle(path); title != "" {
		return title
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func readTagTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	m, err := tag.ReadFrom(f)
	if err != nil {
		return ""
	}

	title := strings.TrimSpace(m.Title())
	artist := strings.TrimSpace(m.Artist())
	if title != "" && artist != "" {
		return artist + " - " + title
	}
	if title != "" {
		return title
	}
	return artist
}
