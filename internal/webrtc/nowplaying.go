package webrtc

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pion/webrtc/v4/pkg/media/oggreader"
)

type TrackMeta struct {
	Path    string
	Title   string
	Artists []string
}

// PublishNowPlaying updates the shared metadata used by /status.
func PublishNowPlaying(title string, artists []string) {
	if str == nil {
		return
	}
	str.nowPlayingLock.Lock()
	str.nowPlayingTitle = title

	dst := make([]string, 0, len(artists))
	dst = append(dst, artists...)
	str.nowPlayingArtists = dst

	str.nowPlayingLock.Unlock()
}

func CurrentNowPlaying() (title string, artists []string) {
	if str == nil {
		return "", []string{}
	}
	str.nowPlayingLock.RLock()
	title = str.nowPlayingTitle

	out := make([]string, 0, len(str.nowPlayingArtists))
	out = append(out, str.nowPlayingArtists...)
	str.nowPlayingLock.RUnlock()
	return title, out
}

// LoadOpusPlaylist returns all *.opus (Ogg Opus) files in mediaDir,
// with best-effort Title/Artist extracted from OpusTags.
// This is unsorted for now, but will be sorted in the future.
func LoadOpusPlaylist(mediaDir string) ([]TrackMeta, error) {
	if mediaDir == "" {
		mediaDir = "media"
	}

	entries, err := os.ReadDir(mediaDir)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".opus" { // ONLY .opus
			paths = append(paths, filepath.Join(mediaDir, e.Name()))
		}
	}

	if len(paths) == 0 {
		return nil, errors.New("no .opus files found")
	}

	out := make([]TrackMeta, 0, len(paths))
	for _, p := range paths {
		title, artists := readOpusTagsBestEffort(p)
		if title == "" {
			title = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		}
		out = append(out, TrackMeta{
			Path:    p,
			Title:   title,
			Artists: artists,
		})
	}

	return out, nil
}

func readOpusTagsBestEffort(path string) (title string, artists []string) {
	f, err := os.Open(path)
	if err != nil {
		return "", []string{}
	}
	defer func() { _ = f.Close() }()

	r, err := oggreader.NewWithOptions(f, oggreader.WithDoChecksum(false))
	if err != nil {
		return "", []string{}
	}

	var artistVals []string

	for {
		payload, pageHeader, err := r.ParseNextPage()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || pageHeader == nil || len(payload) < 8 {
			break
		}

		ht, ok := pageHeader.HeaderType(payload)
		if !ok || ht != oggreader.HeaderOpusTags {
			continue
		}

		tags, err := oggreader.ParseOpusTags(payload)
		if err != nil {
			break
		}

		for _, c := range tags.UserComments {
			key := strings.ToLower(strings.TrimSpace(c.Comment))
			val := strings.TrimSpace(c.Value)

			switch key {
			case "title":
				if title == "" && val != "" {
					title = val
				}
			case "artist":
				if val != "" {
					artistVals = append(artistVals, val)
				}
			}
		}

		break
	}

	// normalize + de-dupe artists (never return nil)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(artistVals))
	for _, v := range artistVals {
		for _, a := range splitArtists(v) {
			if a == "" {
				continue
			}
			if _, ok := seen[a]; ok {
				continue
			}
			seen[a] = struct{}{}
			out = append(out, a)
		}
	}

	return title, out
}

func splitArtists(v string) []string {
	s := strings.TrimSpace(v)
	if s == "" {
		return nil
	}

	// avoid splitting on commas in case artist names contain commas
	seps := []string{" feat. ", " ft. ", " featuring ", ";", " & ", "/", " x "}
	out := []string{s}
	for _, sep := range seps {
		var next []string
		for _, cur := range out {
			parts := strings.Split(cur, sep)
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					next = append(next, p)
				}
			}
		}
		out = next
	}
	return out
}
