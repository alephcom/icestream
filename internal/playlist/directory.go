package playlist

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Track struct {
	Path string
}

type Options struct {
	Paths      []string
	Recursive  bool
	Shuffle    bool
	Loop       bool
	Extension  string
}

type Iterator struct {
	opts     Options
	all      []Track
	order    []int
	position int
	exhausted bool
}

func Scan(opts Options) ([]Track, error) {
	ext := strings.ToLower(opts.Extension)
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}

	seen := make(map[string]struct{})
	var tracks []Track

	for _, root := range opts.Paths {
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("playlist path %q: %w", root, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("playlist path %q is not a directory", root)
		}

		walkFn := func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != root && !opts.Recursive {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.EqualFold(filepath.Ext(path), ext) {
				return nil
			}
			if _, err := os.Stat(path); err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			if _, ok := seen[abs]; ok {
				return nil
			}
			seen[abs] = struct{}{}
			tracks = append(tracks, Track{Path: abs})
			return nil
		}

		if err := filepath.WalkDir(root, walkFn); err != nil {
			return nil, fmt.Errorf("scan %q: %w", root, err)
		}
	}

	if len(tracks) == 0 {
		return nil, fmt.Errorf("no %s files found in playlist paths", ext)
	}

	sort.Slice(tracks, func(i, j int) bool {
		return strings.ToLower(tracks[i].Path) < strings.ToLower(tracks[j].Path)
	})

	return tracks, nil
}

func NewIterator(tracks []Track, opts Options) *Iterator {
	it := &Iterator{
		opts: opts,
		all:  append([]Track(nil), tracks...),
	}
	it.resetOrder()
	return it
}

func (it *Iterator) resetOrder() {
	it.order = make([]int, len(it.all))
	for i := range it.order {
		it.order[i] = i
	}
	if it.opts.Shuffle {
		shuffleIndices(it.order)
	}
	it.position = 0
	it.exhausted = len(it.all) == 0
}

func (it *Iterator) Next() (Track, bool) {
	if it.exhausted || len(it.all) == 0 {
		return Track{}, false
	}

	if it.position >= len(it.order) {
		if !it.opts.Loop {
			it.exhausted = true
			return Track{}, false
		}
		it.resetOrder()
	}

	track := it.all[it.order[it.position]]
	it.position++
	return track, true
}
