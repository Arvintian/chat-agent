package mcp

import (
	"testing"
)

func TestToolFiltered(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		include []string
		exclude []string
		want    bool
	}{
		{name: "no filters keeps", tool: "search", want: true},
		{name: "include exact match", tool: "search", include: []string{"search", "fetch"}, want: true},
		{name: "include exact miss", tool: "upload", include: []string{"search", "fetch"}, want: false},
		{name: "exclude exact match", tool: "upload", exclude: []string{"upload"}, want: false},
		{name: "exclude exact miss", tool: "upload", exclude: []string{"search"}, want: true},
		{name: "include regex re: prefix", tool: "search_web", include: []string{"re:^search_"}, want: true},
		{name: "include regex re: prefix miss", tool: "fetch", include: []string{"re:^search_"}, want: false},
		{name: "include regex slash wrap", tool: "search_web", include: []string{"/^search_.*$/"}, want: true},
		{name: "include regex slash wrap miss", tool: "fetch", include: []string{"/^search_.*/"}, want: false},
		{name: "exclude regex re: prefix", tool: "search_web", exclude: []string{"re:^search_"}, want: false},
		{name: "exclude regex slash wrap", tool: "search_web", exclude: []string{"/^search_.*/"}, want: false},
		{name: "exact name with regex metachars stays literal", tool: "a.b", include: []string{"a.b"}, want: true},
		{name: "invalid regex falls back to exact", tool: "a.b", include: []string{"re:[invalid"}, want: false},
		{name: "regex unanchored matches substring", tool: "xsearch", include: []string{"re:search"}, want: true},
		{name: "regex anchored match works", tool: "xsearch", include: []string{"/^search/"}, want: false},
		{name: "include and exclude combined", tool: "search_web", include: []string{"/^search_.*/"}, exclude: []string{"re:.*_web$"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolFiltered(tt.tool, tt.include, tt.exclude); got != tt.want {
				t.Errorf("toolFiltered(%q, %v, %v) = %v, want %v", tt.tool, tt.include, tt.exclude, got, tt.want)
			}
		})
	}
}
