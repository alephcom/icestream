package playlist_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/darrenwiebe/icestream/internal/playlist"
)

func createTrackDir(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestScanFindsMP3Files(t *testing.T) {
	dir := createTrackDir(t, "a.mp3", "b.MP3", "ignore.txt")
	tracks, err := playlist.Scan(playlist.Options{
		Paths:     []string{dir},
		Recursive: false,
		Extension: ".mp3",
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}
}

func TestScanRecursive(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "deep.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	nonRecursive, err := playlist.Scan(playlist.Options{
		Paths:     []string{dir},
		Recursive: false,
		Extension: ".mp3",
	})
	if err == nil {
		t.Fatal("expected error when no files in root")
	}
	_ = nonRecursive

	tracks, err := playlist.Scan(playlist.Options{
		Paths:     []string{dir},
		Recursive: true,
		Extension: ".mp3",
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(tracks))
	}
}

func TestIteratorSequential(t *testing.T) {
	tracks := []playlist.Track{{Path: "a.mp3"}, {Path: "b.mp3"}, {Path: "c.mp3"}}
	it := playlist.NewIterator(tracks, playlist.Options{Shuffle: false, Loop: false})

	var got []string
	for {
		track, ok := it.Next()
		if !ok {
			break
		}
		got = append(got, track.Path)
	}
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
}

func TestIteratorNoLoopStops(t *testing.T) {
	tracks := []playlist.Track{{Path: "a.mp3"}}
	it := playlist.NewIterator(tracks, playlist.Options{Shuffle: false, Loop: false})

	if _, ok := it.Next(); !ok {
		t.Fatal("expected first track")
	}
	if _, ok := it.Next(); ok {
		t.Fatal("expected stop without loop")
	}
}

func TestIteratorLoopRepeats(t *testing.T) {
	tracks := []playlist.Track{{Path: "a.mp3"}}
	it := playlist.NewIterator(tracks, playlist.Options{Shuffle: false, Loop: true})

	for i := 0; i < 3; i++ {
		if _, ok := it.Next(); !ok {
			t.Fatalf("expected track on iteration %d", i)
		}
	}
}

func TestIteratorShuffleNoRepeatWithinCycle(t *testing.T) {
	tracks := make([]playlist.Track, 10)
	for i := range tracks {
		tracks[i] = playlist.Track{Path: filepath.Join("track", string(rune('a'+i))+".mp3")}
	}
	it := playlist.NewIterator(tracks, playlist.Options{Shuffle: true, Loop: false})

	seen := make(map[string]bool)
	for {
		track, ok := it.Next()
		if !ok {
			break
		}
		if seen[track.Path] {
			t.Fatalf("repeat within cycle: %s", track.Path)
		}
		seen[track.Path] = true
	}
	if len(seen) != len(tracks) {
		t.Fatalf("played %d tracks, want %d", len(seen), len(tracks))
	}
}
