package stream

import (
	"testing"
)

func TestClassifyChapters_ExplicitLabels(t *testing.T) {
	chapters := []ChapterMarker{
		{Title: "Prologue", StartTime: 0.0, EndTime: 120.0},
		{Title: "Opening Theme", StartTime: 120.0, EndTime: 210.0},
		{Title: "Episode", StartTime: 210.0, EndTime: 1200.0},
		{Title: "End Credits", StartTime: 1200.0, EndTime: 1260.0},
	}

	segs := ClassifyChaptersToSkipSegments(chapters, 1260.0, true)
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segs))
	}

	if segs[0].Type != "intro" || segs[0].StartTime != 120.0 || segs[0].EndTime != 210.0 {
		t.Errorf("unexpected intro segment: %+v", segs[0])
	}
	if segs[1].Type != "credits" || segs[1].StartTime != 1200.0 || segs[1].EndTime != 1260.0 {
		t.Errorf("unexpected credits segment: %+v", segs[1])
	}
}

func TestClassifyChapters_SopranosGenericLabels(t *testing.T) {
	// The Sopranos S02E05 actual chapter layout
	chapters := []ChapterMarker{
		{Title: "Chapter 01", StartTime: 0.0, EndTime: 99.724},
		{Title: "Chapter 02", StartTime: 99.724, EndTime: 579.412},
		{Title: "Chapter 03", StartTime: 579.412, EndTime: 1049.173},
		{Title: "Chapter 04", StartTime: 1049.173, EndTime: 1366.656},
		{Title: "Chapter 05", StartTime: 1366.656, EndTime: 1790.413},
		{Title: "Chapter 06", StartTime: 1790.413, EndTime: 2369.033},
		{Title: "Chapter 07", StartTime: 2369.033, EndTime: 3052.674},
		{Title: "Chapter 08", StartTime: 3052.674, EndTime: 3115.680},
	}

	segs := ClassifyChaptersToSkipSegments(chapters, 3115.680, true)
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments for Sopranos, got %d", len(segs))
	}

	intro := segs[0]
	if intro.Type != "intro" || intro.StartTime != 0.0 || intro.EndTime != 99.724 {
		t.Errorf("unexpected Sopranos intro: %+v", intro)
	}

	credits := segs[1]
	if credits.Type != "credits" || credits.StartTime != 3052.674 || credits.EndTime != 3115.680 {
		t.Errorf("unexpected Sopranos credits: %+v", credits)
	}
}

func TestClassifyChapters_ColdOpenGenericLabels(t *testing.T) {
	// Cold open in Chapter 01 (180s), Intro in Chapter 02 (90s)
	chapters := []ChapterMarker{
		{Title: "Chapter 01", StartTime: 0.0, EndTime: 180.0},
		{Title: "Chapter 02", StartTime: 180.0, EndTime: 270.0},
		{Title: "Chapter 03", StartTime: 270.0, EndTime: 1500.0},
		{Title: "Chapter 04", StartTime: 1500.0, EndTime: 1560.0},
	}

	segs := ClassifyChaptersToSkipSegments(chapters, 1560.0, true)
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments for Cold Open, got %d", len(segs))
	}

	if segs[0].Type != "intro" || segs[0].StartTime != 180.0 || segs[0].EndTime != 270.0 {
		t.Errorf("unexpected cold open intro: %+v", segs[0])
	}
	if segs[1].Type != "credits" || segs[1].StartTime != 1500.0 || segs[1].EndTime != 1560.0 {
		t.Errorf("unexpected cold open credits: %+v", segs[1])
	}
}
