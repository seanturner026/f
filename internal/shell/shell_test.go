package shell_test

import (
	"testing"

	"github.com/seanturner026/f/internal/shell"
)

func TestQuote(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{name: "plain", in: "pi", want: `'pi'`},
		{name: "spaces", in: "a b", want: `'a b'`},
		{name: "single quote", in: "it's", want: `'it'\''s'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shell.Quote(tt.in); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}
