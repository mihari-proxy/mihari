package runtime

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"sync"
	"unicode/utf8"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

const (
	previewSegmentBytes    = 32 << 10
	previewPageBytes       = 1 << 20
	cachedOverridePreviews = 4
)

// A snapshot retains the masked or explicitly revealed view already generated for its
// draft. Encoded byte offsets permit random-access continuation without re-rendering.
type previewSnapshot struct {
	header      protocol.ConfigOverridesPreview
	segments    []protocol.OverridePreviewLine
	encodedEnds []int64
	budget      int64
}

// Only display metadata is abbreviated; every byte of the YAML text is paged.
func previewLabel(text string) string {
	if len(text) <= previewSegmentBytes {
		return text
	}
	end := previewSegmentBytes - len("…")
	for !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + "…"
}

func newPreviewSnapshot(ctx context.Context, header protocol.ConfigOverridesPreview, lines []protocol.ProxyAuthPreviewLine) (*previewSnapshot, error) {
	encodedHeader, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	// Reserve space for the optional next offset, final newline, and array delimiters.
	snap := &previewSnapshot{header: header, budget: int64(previewPageBytes - len(encodedHeader) - 64)}
	var used int64
	for _, line := range lines {
		segment := protocol.OverridePreviewLine{Field: previewLabel(line.Field), Target: previewLabel(line.Target), Source: previewLabel(line.Source)}
		switch line.Change {
		case "+":
			segment.Change = "added"
		case "-":
			segment.Change = "removed"
		}
		text := line.Text
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			end := min(len(text), previewSegmentBytes)
			for end < len(text) && !utf8.RuneStart(text[end]) {
				end--
			}
			segment.Text = text[:end]
			encoded, err := json.Marshal(segment)
			if err != nil {
				return nil, err
			}
			used += int64(len(encoded) + 1)
			snap.segments = append(snap.segments, segment)
			snap.encodedEnds = append(snap.encodedEnds, used)
			text = text[end:]
			if text == "" {
				break
			}
			segment.Continued = true
		}
	}
	return snap, nil
}

func (s *previewSnapshot) page(offset int) (protocol.ConfigOverridesPreview, error) {
	if offset < 0 || offset > len(s.segments) {
		return protocol.ConfigOverridesPreview{}, invalidPreview("preview offset exceeds available content")
	}
	budget := s.budget
	if offset > 0 {
		budget += s.encodedEnds[offset-1]
	}
	end := offset + sort.Search(len(s.segments)-offset, func(i int) bool { return s.encodedEnds[offset+i] > budget })
	result := s.header
	result.Lines = slices.Clone(s.segments[offset:end])
	if end < len(s.segments) {
		result.NextOffset = end
	}
	return result, nil
}

type overridePreviewCache struct {
	mu      sync.Mutex
	entries []*previewSnapshot // Most recently read first.
}

func (c *overridePreviewCache) get(key string) *previewSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, s := range c.entries {
		if s.header.Snapshot == key {
			copy(c.entries[1:i+1], c.entries[:i])
			c.entries[0] = s
			return s
		}
	}
	return nil
}
func (c *overridePreviewCache) put(key string, s *previewSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := slices.IndexFunc(c.entries, func(e *previewSnapshot) bool { return e.header.Snapshot == key })
	if i < 0 {
		if len(c.entries) < cachedOverridePreviews {
			c.entries = append(c.entries, nil)
		}
		i = len(c.entries) - 1
	}
	copy(c.entries[1:i+1], c.entries[:i])
	c.entries[0] = s
}
