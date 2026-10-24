package app

import (
	"strings"
	"testing"

	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
)

func validationCandidates() map[string]model.Video {
	return map[string]model.Video{
		"BV1Wbnh6jEhs": {BVID: "BV1Wbnh6jEhs", Title: "picked"},
		"BV1VAnb64ERJ": {BVID: "BV1VAnb64ERJ", Title: "skipped"},
	}
}

func validRunSummary() model.RunSummary {
	return model.RunSummary{
		RunID: "run-1",
		Picked: []model.PickedItem{{
			BVID: "BV1Wbnh6jEhs", Title: "picked", Author: "author",
			URL:  "https://www.bilibili.com/video/BV1Wbnh6jEhs",
			Tier: "值得看", Reason: "subtitle supports the selection",
			OneLiner: "useful summary",
		}},
		Skipped: []model.SkippedItem{{
			BVID: "BV1VAnb64ERJ", Reason: "no usable subtitle",
		}},
		Summary:         "one picked and one skipped",
		NoSubtitleCount: 1,
	}
}

func TestValidateRunSummaryAcceptsCompleteCoverage(t *testing.T) {
	if err := validateRunSummary(validRunSummary(), validationCandidates()); err != nil {
		t.Fatalf("valid summary rejected: %v", err)
	}
}

func TestValidateRunSummaryRejectsIncompleteOrAmbiguousData(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*model.RunSummary)
		wantErr string
	}{
		{
			name: "missing summary",
			mutate: func(summary *model.RunSummary) {
				summary.Summary = " "
			},
			wantErr: "summary must describe",
		},
		{
			name: "missing picked tier",
			mutate: func(summary *model.RunSummary) {
				summary.Picked[0].Tier = ""
			},
			wantErr: "invalid tier",
		},
		{
			name: "missing picked reason",
			mutate: func(summary *model.RunSummary) {
				summary.Picked[0].Reason = ""
			},
			wantErr: "missing reason",
		},
		{
			name: "missing picked one liner",
			mutate: func(summary *model.RunSummary) {
				summary.Picked[0].OneLiner = ""
			},
			wantErr: "missing one_liner",
		},
		{
			name: "missing skipped reason",
			mutate: func(summary *model.RunSummary) {
				summary.Skipped[0].Reason = ""
			},
			wantErr: "missing reason",
		},
		{
			name: "duplicate classification",
			mutate: func(summary *model.RunSummary) {
				summary.Skipped[0].BVID = summary.Picked[0].BVID
			},
			wantErr: "appears more than once",
		},
		{
			name: "omitted candidate",
			mutate: func(summary *model.RunSummary) {
				summary.Skipped = nil
				summary.NoSubtitleCount = 0
			},
			wantErr: "summary omits candidate",
		},
		{
			name: "invalid subtitle count",
			mutate: func(summary *model.RunSummary) {
				summary.NoSubtitleCount = 2
			},
			wantErr: "no_subtitle_count",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			summary := validRunSummary()
			test.mutate(&summary)
			err := validateRunSummary(summary, validationCandidates())
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("got %v, want substring %q", err, test.wantErr)
			}
		})
	}
}
