package main

import (
	"slices"
	"testing"
)

func TestParseFzfOutput(t *testing.T) {
	tests := []struct {
		name   string
		out    string
		expect bool
		want   []string
	}{
		{name: "enter with expect keeps empty key", out: "\nwebapp\tline\n", expect: true, want: []string{"", "webapp\tline"}},
		{name: "expected key", out: "ctrl-x\nwebapp\tline\n", expect: true, want: []string{"ctrl-x", "webapp\tline"}},
		{name: "plain selection", out: "a\nb\n", want: []string{"a", "b"}},
		{name: "drops empty lines without expect", out: "\na\n", want: []string{"a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseFzfOutput(tt.out, tt.expect); !slices.Equal(got, tt.want) {
				t.Errorf("parseFzfOutput(%q, %v) = %q, want %q", tt.out, tt.expect, got, tt.want)
			}
		})
	}
}
