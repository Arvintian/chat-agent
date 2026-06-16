package serve

// ConcatToolArguments accumulates incremental tool call arguments.
// When streaming=true, each SendToolCall carries a delta of the arguments
// JSON string. Callers should use this function to build the complete
// arguments string by concatenating successive deltas for the same tool call.
func ConcatToolArguments(existing, delta string) string {
	return existing + delta
}
