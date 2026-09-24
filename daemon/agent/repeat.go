package agent

import (
	"strings"
)

// A local model can fall into writing one passage over and over. In 01M39RT5
// it wrote the same paragraph 88 times in its thinking and 265 times in its
// reply, for 7.5 minutes, until the token cap stopped it. The cap is a bound,
// not a detector: by then the reply is 50 KB of the same text, and it goes
// back to the model on the next turn.
//
// repeatWatch sees the repetition while it streams, within a few copies.

const (
	// repeatMinSpan is the shortest passage that counts. Shorter periods are
	// rules, padding and tables, not a model repeating itself.
	repeatMinSpan = 40
	// repeatEvery is how many new bytes arrive between checks.
	repeatEvery = 256
	// repeatWindow bounds how far back a check looks.
	repeatWindow = 64 << 10
)

// repeatWatch accumulates one stream, the reply or the thinking, and reports
// when its end has become the same passage repeated limit times.
type repeatWatch struct {
	limit   int // copies in a row that trip it; below 1, never
	text    strings.Builder
	checked int
	// Set when tripped.
	period, start int
}

func newRepeatWatch(limit int) *repeatWatch { return &repeatWatch{limit: limit} }

// add appends streamed text and reports whether the watch has tripped.
func (w *repeatWatch) add(s string) bool {
	if w == nil || w.limit < 1 || w.period > 0 {
		return w != nil && w.period > 0
	}
	w.text.WriteString(s)
	if w.text.Len()-w.checked < repeatEvery {
		return false
	}
	w.checked = w.text.Len()
	return w.check()
}

// check looks for the smallest period of the text's end. The last
// repeatMinSpan bytes recur one period back, so each earlier occurrence of
// them is a candidate, nearest first. The first that holds for limit copies
// is the smallest period; it trips the watch only if it is a passage.
func (w *repeatWatch) check() bool {
	text := w.text.String()
	n := len(text)
	lo := max(0, n-repeatWindow)
	needle := text[n-repeatMinSpan:]
	hay := text[lo : n-1]
	for {
		i := strings.LastIndex(hay, needle)
		if i < 0 {
			return false
		}
		p := n - repeatMinSpan - (lo + i)
		hay = hay[:i+repeatMinSpan-1]
		span := w.limit * max(p, repeatMinSpan)
		if span > n {
			continue
		}
		if text[n-span:n-p] != text[n-span+p:] {
			continue
		}
		if p < repeatMinSpan {
			return false
		}
		w.period = p
		// Walk back to where the repetition began.
		w.start = n - span
		for w.start > 0 && text[w.start-1] == text[w.start-1+p] {
			w.start--
		}
		return true
	}
}

// kept is the stream up to where the repetition began, plus one copy.
func (w *repeatWatch) kept() string {
	text := w.text.String()
	if w.period == 0 {
		return text
	}
	return strings.ToValidUTF8(text[:w.start+w.period], "")
}

// repeats is how many copies had arrived when the watch tripped.
func (w *repeatWatch) repeats() int {
	if w.period == 0 {
		return 0
	}
	return (w.text.Len() - w.start) / w.period
}

// passage is the repeated text.
func (w *repeatWatch) passage() string {
	if w.period == 0 {
		return ""
	}
	return strings.ToValidUTF8(w.text.String()[w.start:w.start+w.period], "")
}

// length is how much of the stream arrived.
func (w *repeatWatch) length() int { return w.text.Len() }
