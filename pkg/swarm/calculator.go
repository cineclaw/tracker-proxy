package swarm

import (
	"strings"
)

// SwarmAssessment represents the consolidated swarm health and speed metrics.
type SwarmAssessment struct {
	EffectiveSeeds int    `json:"effective_seeds"`
	RealSeeds      int    `json:"real_seeds"`
	SeedsVerified  bool   `json:"seeds_verified"`
	Completed      int    `json:"completed"`
	Leeches        int    `json:"leeches"`
	SwarmHealth    string `json:"swarm_health"`    // "dead" | "low" | "good" | "excellent"
	SpeedPotential int    `json:"speed_potential"` // 0 - 100
	Reason         string `json:"reason,omitempty"`
}

// AssessmentParams holds inputs for calculating swarm health and speed potential.
type AssessmentParams struct {
	ClaimedSeeds             int
	ClaimedLeeches           int
	VerifiedSeeds            int
	VerifiedLeeches          int
	VerifiedCompleted        int
	Trackers                 []string
	SizeBytes                int64
	Resolution               string
	WasScraped               bool
	AllTrackersReturnedZero  bool
}

// CalculateHealthAndPotential deterministically evaluates swarm vitality and real-time streaming capability.
func CalculateHealthAndPotential(p AssessmentParams) SwarmAssessment {
	hasRuTracker := false
	hasNNM := false
	for _, tr := range p.Trackers {
		lower := strings.ToLower(tr)
		if strings.Contains(lower, "rutracker") {
			hasRuTracker = true
		} else if strings.Contains(lower, "nnm") {
			hasNNM = true
		}
	}

	var effectiveSeeds int
	var realSeeds int
	var seedsVerified bool
	var completed = p.VerifiedCompleted
	var leeches = p.ClaimedLeeches
	if p.VerifiedLeeches > leeches {
		leeches = p.VerifiedLeeches
	}
	var reason string

	if p.WasScraped {
		seedsVerified = true
		if p.VerifiedSeeds > 0 {
			// Real active seeds confirmed by open trackers
			realSeeds = p.VerifiedSeeds
			effectiveSeeds = p.VerifiedSeeds
			if hasRuTracker && p.ClaimedSeeds > effectiveSeeds {
				// RuTracker's passkey tracker bt.t-ru.org may have additional private seeds
				effectiveSeeds = p.ClaimedSeeds
			}
			reason = "verified_live_swarm"
		} else if p.AllTrackersReturnedZero {
			if hasRuTracker && p.ClaimedSeeds > 0 {
				// RuTracker announce is private HTTP and doesn't federate with open UDP trackers.
				// Trust RuTracker authenticated seeds.
				effectiveSeeds = p.ClaimedSeeds
				realSeeds = p.ClaimedSeeds
				reason = "rutracker_authenticated_trust"
			} else {
				// Confirmed ghost / dead swarm (RuTor claimed seeds refuted by responsive open trackers)
				effectiveSeeds = 0
				realSeeds = 0
				completed = 0
				reason = "ghost_swarm_refuted_by_trackers"
			}
		} else {
			// Scrape attempt had partial response or inconclusive results
			if hasRuTracker {
				effectiveSeeds = p.ClaimedSeeds
				realSeeds = p.ClaimedSeeds
			} else if hasNNM {
				effectiveSeeds = int(float64(p.ClaimedSeeds) * 0.85)
				realSeeds = effectiveSeeds
			} else {
				effectiveSeeds = int(float64(p.ClaimedSeeds) * 0.50)
				realSeeds = effectiveSeeds
			}
			reason = "inconclusive_scrape_fallback"
		}
	} else {
		// Not scraped via UDP (e.g. timeout, missing hash, or scrape disabled)
		seedsVerified = false
		if hasRuTracker {
			effectiveSeeds = p.ClaimedSeeds
			reason = "rutracker_claimed_trust"
		} else if hasNNM {
			effectiveSeeds = int(float64(p.ClaimedSeeds) * 0.85)
			reason = "nnmclub_claimed_discount"
		} else {
			effectiveSeeds = int(float64(p.ClaimedSeeds) * 0.50)
			reason = "rutor_unverified_discount"
		}
		realSeeds = effectiveSeeds
	}

	// Calculate Speed Potential (0 - 100)
	var speedPotential int
	if effectiveSeeds <= 0 {
		speedPotential = 0
	} else {
		// 1. Base score from effective seeders count
		switch {
		case effectiveSeeds == 1:
			speedPotential = 30
		case effectiveSeeds == 2:
			speedPotential = 45
		case effectiveSeeds == 3:
			speedPotential = 58
		case effectiveSeeds <= 5:
			speedPotential = 68
		case effectiveSeeds <= 10:
			speedPotential = 80
		case effectiveSeeds <= 20:
			speedPotential = 88
		case effectiveSeeds <= 40:
			speedPotential = 94
		default:
			bonus := (effectiveSeeds - 40) / 30
			if bonus > 6 {
				bonus = 6
			}
			speedPotential = 94 + bonus
		}

		// 2. Swarm contention / congestion penalty (Leechers vs Seeds)
		if effectiveSeeds > 0 && leeches > effectiveSeeds*2 {
			ratio := leeches / effectiveSeeds
			penalty := ratio * 2
			if penalty > 20 {
				penalty = 20
			}
			speedPotential -= penalty
		} else if effectiveSeeds >= leeches && effectiveSeeds >= 5 {
			speedPotential += 4 // idle seed capacity bonus
		}

		// 3. Swarm maturity / chunk availability bonus (Completed count)
		if completed >= 1000 {
			speedPotential += 6
		} else if completed >= 100 {
			speedPotential += 3
		}

		// 4. Bitrate Feasibility Constraints (4K vs 1080p)
		resLower := strings.ToLower(p.Resolution)
		is4K := resLower == "4k" || resLower == "2160p" || p.SizeBytes > 25*1024*1024*1024
		if is4K {
			// 4K REMUX requires sustained ~35-80 Mbps download rate
			if effectiveSeeds < 3 {
				if speedPotential > 35 {
					speedPotential = 35 // high risk of buffer underrun
				}
			} else if effectiveSeeds < 6 {
				if speedPotential > 65 {
					speedPotential = 65
				}
			}
		} else if resLower == "1080p" && effectiveSeeds >= 5 {
			if speedPotential < 70 {
				speedPotential = 70
			}
		}

		// Clamp to 0..100
		if speedPotential < 0 {
			speedPotential = 0
		} else if speedPotential > 100 {
			speedPotential = 100
		}
	}

	// Determine Swarm Health Rating
	var swarmHealth string
	switch {
	case effectiveSeeds == 0 || speedPotential < 15:
		swarmHealth = "dead"
	case speedPotential < 46:
		swarmHealth = "low"
	case speedPotential < 80:
		swarmHealth = "good"
	default:
		swarmHealth = "excellent"
	}

	return SwarmAssessment{
		EffectiveSeeds: effectiveSeeds,
		RealSeeds:      realSeeds,
		SeedsVerified:  seedsVerified,
		Completed:      completed,
		Leeches:        leeches,
		SwarmHealth:    swarmHealth,
		SpeedPotential: speedPotential,
		Reason:         reason,
	}
}
