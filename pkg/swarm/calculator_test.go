package swarm

import (
	"testing"
)

func TestCalculateHealthAndPotential_GhostSwarm(t *testing.T) {
	// RuTor claims 150 seeds, but open trackers return 0 seeds & 0 completed
	p := AssessmentParams{
		ClaimedSeeds:            150,
		ClaimedLeeches:          10,
		VerifiedSeeds:           0,
		VerifiedLeeches:         0,
		VerifiedCompleted:       0,
		Trackers:                []string{"rutor"},
		SizeBytes:               4 * 1024 * 1024 * 1024,
		Resolution:              "1080p",
		WasScraped:              true,
		AllTrackersReturnedZero: true,
	}

	a := CalculateHealthAndPotential(p)
	if a.EffectiveSeeds != 0 {
		t.Errorf("expected 0 effective seeds for ghost swarm, got %d", a.EffectiveSeeds)
	}
	if a.RealSeeds != 0 {
		t.Errorf("expected 0 real seeds, got %d", a.RealSeeds)
	}
	if !a.SeedsVerified {
		t.Errorf("expected seeds to be verified as refuted, got false")
	}
	if a.SwarmHealth != "dead" {
		t.Errorf("expected dead swarm health, got %s", a.SwarmHealth)
	}
	if a.SpeedPotential != 0 {
		t.Errorf("expected 0 speed potential, got %d", a.SpeedPotential)
	}
}

func TestCalculateHealthAndPotential_VerifiedHealthySwarm(t *testing.T) {
	// RuTor claimed 5 seeds, but open tracker verified 45 seeds and 1500 completed
	p := AssessmentParams{
		ClaimedSeeds:            5,
		ClaimedLeeches:          2,
		VerifiedSeeds:           45,
		VerifiedLeeches:         4,
		VerifiedCompleted:       1500,
		Trackers:                []string{"rutor"},
		SizeBytes:               12 * 1024 * 1024 * 1024,
		Resolution:              "1080p",
		WasScraped:              true,
		AllTrackersReturnedZero: false,
	}

	a := CalculateHealthAndPotential(p)
	if a.EffectiveSeeds != 45 {
		t.Errorf("expected 45 effective seeds, got %d", a.EffectiveSeeds)
	}
	if a.RealSeeds != 45 {
		t.Errorf("expected 45 real seeds, got %d", a.RealSeeds)
	}
	if !a.SeedsVerified {
		t.Errorf("expected seeds to be verified, got false")
	}
	if a.SwarmHealth != "excellent" {
		t.Errorf("expected excellent swarm health, got %s", a.SwarmHealth)
	}
	if a.SpeedPotential < 90 {
		t.Errorf("expected >= 90 speed potential, got %d", a.SpeedPotential)
	}
}

func TestCalculateHealthAndPotential_RuTrackerPrivateSwarm(t *testing.T) {
	// RuTracker claimed 25 seeds, but open trackers return 0 (not federated)
	p := AssessmentParams{
		ClaimedSeeds:            25,
		ClaimedLeeches:          3,
		VerifiedSeeds:           0,
		VerifiedLeeches:         0,
		VerifiedCompleted:       0,
		Trackers:                []string{"rutracker"},
		SizeBytes:               8 * 1024 * 1024 * 1024,
		Resolution:              "1080p",
		WasScraped:              true,
		AllTrackersReturnedZero: true,
	}

	a := CalculateHealthAndPotential(p)
	if a.EffectiveSeeds != 25 {
		t.Errorf("expected RuTracker seeds to be trusted (25), got %d", a.EffectiveSeeds)
	}
	if a.SwarmHealth != "excellent" {
		t.Errorf("expected excellent swarm health for 25 seeds, got %s", a.SwarmHealth)
	}
}

func TestCalculateHealthAndPotential_4KLowSeedBufferingPenalty(t *testing.T) {
	// 4K REMUX with only 2 seeds -> speed potential must be capped
	p := AssessmentParams{
		ClaimedSeeds:            2,
		ClaimedLeeches:          0,
		VerifiedSeeds:           2,
		VerifiedLeeches:         0,
		VerifiedCompleted:       50,
		Trackers:                []string{"rutor"},
		SizeBytes:               45 * 1024 * 1024 * 1024, // 45 GB REMUX
		Resolution:              "4k",
		WasScraped:              true,
		AllTrackersReturnedZero: false,
	}

	a := CalculateHealthAndPotential(p)
	if a.SpeedPotential > 35 {
		t.Errorf("expected 4K low seed potential to be capped at 35, got %d", a.SpeedPotential)
	}
	if a.SwarmHealth != "low" {
		t.Errorf("expected low swarm health, got %s", a.SwarmHealth)
	}
}
