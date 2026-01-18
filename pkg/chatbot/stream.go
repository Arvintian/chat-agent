package chatbot

import (
	"os"
	"strings"

	"golang.org/x/term"
)

type StreamFilter struct {
	pendingOutput []string
}

func NewStreamFilter() *StreamFilter {
	return &StreamFilter{
		pendingOutput: make([]string, 0),
	}
}

func (f *StreamFilter) Process(chunk string) *string {
	if strings.HasSuffix(chunk, "\n") {
		f.pendingOutput = append(f.pendingOutput, chunk)
		return nil
	} else {
		if len(f.pendingOutput) == 0 {
			return &chunk
		}
		result := strings.Join(f.pendingOutput, "") + chunk
		f.pendingOutput = make([]string, 0)
		return &result
	}
}

func (f *StreamFilter) Finish() *string {
	if len(f.pendingOutput) > 0 {
		result := strings.TrimRight(strings.Join(f.pendingOutput, ""), "\n")
		f.pendingOutput = make([]string, 0)
		return &result
	}
	return nil
}

func TruncateToTermWidth(s string) (string, bool) {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		width = 80
	}
	availableWidth := int(float64(width) * 0.9)
	if availableWidth < 1 {
		availableWidth = 1
	}
	if len(s) <= availableWidth {
		return s, false
	}
	if availableWidth <= 3 {
		return strings.Repeat(".", availableWidth), true
	}
	frontKeep := int(float64(availableWidth-3) * 0.8)
	backKeep := availableWidth - 3 - frontKeep
	return s[:frontKeep] + "..." + s[len(s)-backKeep:], true
}
