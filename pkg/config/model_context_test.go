package config

import "testing"

func TestModelContextSize(t *testing.T) {
	tests := []struct {
		name string
		model Model
		want int
	}{
		{
			name:  "not configured",
			model: Model{ModelParams: ModelParams{Provider: "p", Model: "m"}},
			want:  0,
		},
		{
			name:  "single model configured",
			model: Model{ModelParams: ModelParams{Provider: "p", Model: "m", MaxContextTokens: 65536}},
			want:  65536,
		},
		{
			name: "mixed takes smallest configured window",
			model: Model{
				ModelParams: ModelParams{MaxContextTokens: 128000},
				Mixed: []MixedModel{
					{ModelParams: ModelParams{MaxContextTokens: 32000}},
					{ModelParams: ModelParams{MaxContextTokens: 128000}},
					{ModelParams: ModelParams{}}, // unset entries are ignored
				},
			},
			want: 32000,
		},
		{
			name: "mixed with no entry configured falls back to top-level",
			model: Model{
				ModelParams: ModelParams{MaxContextTokens: 64000},
				Mixed:       []MixedModel{{ModelParams: ModelParams{}}},
			},
			want: 64000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.model.ContextSize(); got != tt.want {
				t.Fatalf("ContextSize() = %d, want %d", got, tt.want)
			}
		})
	}
}
