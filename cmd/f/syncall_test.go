package main

import (
	"testing"

	"github.com/seanturner026/pi/internal/model"
)

func TestNewlyMerged(t *testing.T) {
	before := []model.PR{
		{URL: "a", Status: model.PRStatusOpen},
		{URL: "b", Status: model.PRStatusMerged},
		{URL: "c", Status: model.PRStatusOpen},
	}
	after := []model.PR{
		{URL: "a", Status: model.PRStatusMerged}, // newly merged
		{URL: "b", Status: model.PRStatusMerged}, // already merged
		{URL: "c", Status: model.PRStatusOpen},
		{URL: "d", Status: model.PRStatusMerged}, // unknown before: pi just recorded it, skip
	}
	got := newlyMerged(before, after)
	if len(got) != 1 || got[0].URL != "a" {
		t.Errorf("newlyMerged = %+v, want just a", got)
	}
}
