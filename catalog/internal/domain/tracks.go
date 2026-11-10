// The tracks of an audio CD image: one continuous stream, where a track is a position in it.

package domain

import "fmt"

type Track struct {
	N        int
	Title    string
	Artist   string
	Start    float64
	HasIndex bool
}

// Span is a track with its start, end and length in seconds (nil when the end is unknown).
type Span struct {
	N      int
	Title  string
	Artist string
	Start  float64
	End    *float64
	Length *float64
}

// TrackSpans gives the tracks with start, end and length. total overrides the duration (for
// images that are not raw, whose length only VLC knows).
func TrackSpans(tracks []Track, total *float64) []Span {
	out := make([]Span, len(tracks))
	for i, t := range tracks {
		end := total
		if i+1 < len(tracks) {
			e := tracks[i+1].Start
			end = &e
		}
		title := t.Title
		if title == "" {
			title = fmt.Sprintf("Track %d", t.N)
		}
		var length *float64
		if end != nil {
			l := *end - t.Start
			if l < 0 {
				l = 0
			}
			length = &l
		}
		out[i] = Span{N: t.N, Title: title, Artist: t.Artist, Start: t.Start, End: end, Length: length}
	}
	return out
}

// SpansTracks turns spans back into tracks (to compute them again with VLC's length).
func SpansTracks(spans []Span) []Track {
	out := make([]Track, len(spans))
	for i, s := range spans {
		out[i] = Track{N: s.N, Title: s.Title, Artist: s.Artist, Start: s.Start, HasIndex: true}
	}
	return out
}

// Locate gives the index of the track that holds position (whole seconds from the start).
func Locate(spans []Span, position int64) int {
	idx := 0
	for i, t := range spans {
		if position >= int64(t.Start) {
			idx = i
		}
	}
	return idx
}
