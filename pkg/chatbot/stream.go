package chatbot

import "strings"

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
