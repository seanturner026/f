package model_test

import (
	"testing"

	"github.com/seanturner026/pi/internal/model"
)

func TestPRStatusFromGitHub(t *testing.T) {
	tests := []struct {
		name    string
		state   string
		isDraft bool
		want    model.PRStatus
	}{
		{name: "open", state: "OPEN", want: model.PRStatusOpen},
		{name: "draft", state: "OPEN", isDraft: true, want: model.PRStatusDraft},
		{name: "merged", state: "MERGED", want: model.PRStatusMerged},
		{name: "merged draft flag ignored", state: "MERGED", isDraft: true, want: model.PRStatusMerged},
		{name: "closed", state: "CLOSED", want: model.PRStatusClosed},
		{name: "unknown treated as open", state: "WEIRD", want: model.PRStatusOpen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := model.PRStatusFromGitHub(tt.state, tt.isDraft); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPRStatusIsActive(t *testing.T) {
	tests := []struct {
		status model.PRStatus
		want   bool
	}{
		{model.PRStatusOpen, true},
		{model.PRStatusDraft, true},
		{model.PRStatusMerged, false},
		{model.PRStatusClosed, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsActive(); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
