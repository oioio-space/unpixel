// Package unpixel_test — shared types, status constants, and aggregation
// helpers for the journal harness. This file carries no build tag so the
// types are visible to the default test suite (enabling unit tests of
// summariseCorpora / whyToBucket without requiring the journal tag).
package unpixel_test

import "strings"

// ─── result types ─────────────────────────────────────────────────────────────

// journalStatus represents the outcome of a recovery attempt.
type journalStatus string

const (
	statusOK      journalStatus = "ok"
	statusFail    journalStatus = "fail"
	statusUnknown journalStatus = "unknown"
	statusSkipped journalStatus = "skipped"
	statusTimeout journalStatus = "timeout"
	statusError   journalStatus = "error"
)

// journalAttempt holds the outcome of one recovery attempt (zero-config or best-config).
type journalAttempt struct {
	// Params describes the configuration used for this attempt.
	Params string `json:"params"`
	// Guess is the recovered text.
	Guess string `json:"guess"`
	// Status is ok/fail/unknown/skipped/timeout/error.
	Status journalStatus `json:"status"`
	// ExactCI is true when the guess matches ground truth case-insensitively.
	ExactCI bool `json:"exact_ci,omitzero"`
	// Score is the partial-credit Levenshtein score in [0,100], or -1 when
	// ground truth is unknown. See recoveryScore in journal_classify_test.go.
	Score float64 `json:"score"`
	// Confidence is Result.Confidence.
	Confidence float64 `json:"confidence"`
	// BestTotal is Result.BestTotal.
	BestTotal float64 `json:"best_total"`
	// DurationMS is the wall-clock time in milliseconds.
	DurationMS float64 `json:"duration_ms"`
	// Why is a concise human-readable failure reason (non-empty on fail).
	Why string `json:"why,omitempty"`
	// BelowThreshold mirrors Result.BelowThreshold.
	BelowThreshold bool `json:"below_threshold,omitzero"`
	// BlurSigma mirrors Result.BlurSigma (non-zero for blur recovery).
	BlurSigma float64 `json:"blur_sigma,omitzero"`
}

// journalRow is the record for one image across both modes.
type journalRow struct {
	Corpus      string         `json:"corpus"`
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`         // "mosaic" or "blur"
	GroundTruth string         `json:"ground_truth"` // "—" when unknown
	ZeroConfig  journalAttempt `json:"zero_config"`
	BestConfig  journalAttempt `json:"best_config"`
}

// journalCorpusSummary holds per-corpus aggregate counts and partial-credit metrics.
type journalCorpusSummary struct {
	Name        string `json:"name"`
	Total       int    `json:"total"`
	ZeroOK      int    `json:"zero_ok"`
	BestOK      int    `json:"best_ok"`
	ZeroUnknown int    `json:"zero_unknown"`
	BestUnknown int    `json:"best_unknown"`
	// ZeroMeanScore is the mean recoveryScore across all images with known ground
	// truth, zero-config mode. -1 when no scored images exist.
	ZeroMeanScore float64 `json:"zero_mean_score"`
	// BestMeanScore is the same for best-config mode.
	BestMeanScore float64 `json:"best_mean_score"`
	// ZeroSensical is the count of images scoring ≥70% in zero-config mode.
	ZeroSensical int `json:"zero_sensical"`
	// BestSensical is the count of images scoring ≥70% in best-config mode.
	BestSensical int `json:"best_sensical"`

	// BestFailModes is a histogram of failure reasons for best-config mode,
	// keyed by the canonical bucket names derived from classifyOutcome's why
	// strings: "ok", "below-threshold", "wrong-length", "wrong-glyphs",
	// "timeout", "error", "unknown".
	// Images with statusUnknown or statusSkipped are excluded.
	BestFailModes map[string]int `json:"best_fail_modes,omitempty"`

	// ZeroMeanConf is the mean Confidence over images with known ground truth,
	// zero-config mode. -1 when no scored images exist.
	ZeroMeanConf float64 `json:"zero_mean_conf"`
	// BestMeanConf is the same for best-config mode.
	BestMeanConf float64 `json:"best_mean_conf"`

	// ZeroMeanFidelity is the mean of (1 − BestTotal), clamped to [0,1], over
	// images with known ground truth, zero-config mode. -1 when none exist.
	ZeroMeanFidelity float64 `json:"zero_mean_fidelity"`
	// BestMeanFidelity is the same for best-config mode.
	BestMeanFidelity float64 `json:"best_mean_fidelity"`

	// BestDurationMS is the sum of DurationMS over all best-config attempts in
	// this corpus (including skipped / unknown).
	BestDurationMS float64 `json:"best_duration_ms,omitzero"`
}

// ─── aggregation ─────────────────────────────────────────────────────────────

// whyToBucket maps a classifyOutcome why string (and a journalStatus) to one
// of the canonical failure-mode bucket keys used in BestFailModes:
//
//	"ok"               — exact match
//	"below-threshold"  — why starts with "below-threshold"
//	"timeout"          — why starts with "timeout"
//	"wrong-length"     — why starts with "wrong length"
//	"wrong-glyphs"     — why starts with "wrong glyphs"
//	"error"            — status == statusError
//	"unknown"          — anything else (should not occur in practice)
//
// Rows with statusUnknown or statusSkipped are excluded by the caller.
func whyToBucket(status journalStatus, why string) string {
	switch status {
	case statusOK:
		return "ok"
	case statusError:
		return "error"
	}
	switch {
	case strings.HasPrefix(why, "below-threshold"):
		return "below-threshold"
	case strings.HasPrefix(why, "timeout"):
		return "timeout"
	case strings.HasPrefix(why, "wrong length"):
		return "wrong-length"
	case strings.HasPrefix(why, "wrong glyphs"):
		return "wrong-glyphs"
	default:
		return "unknown"
	}
}

func summariseCorpora(rows []journalRow) []journalCorpusSummary {
	byCorpus := make(map[string]*journalCorpusSummary)
	// Preserve a stable corpus order.
	order := []string{"fixtures", "blur", "real", "wild", "sick"}
	newSummary := func(name string) *journalCorpusSummary {
		return &journalCorpusSummary{
			Name:             name,
			ZeroMeanScore:    -1,
			BestMeanScore:    -1,
			ZeroMeanConf:     -1,
			BestMeanConf:     -1,
			ZeroMeanFidelity: -1,
			BestMeanFidelity: -1,
			BestFailModes:    make(map[string]int),
		}
	}
	for _, name := range order {
		byCorpus[name] = newSummary(name)
	}

	// Accumulate sums separately so we can compute means at the end.
	type sums struct {
		zeroScore, bestScore   float64
		zeroConf, bestConf     float64
		zeroFid, bestFid       float64
		zeroScoreN, bestScoreN int
		zeroConfN, bestConfN   int
		zeroFidN, bestFidN     int
	}
	accum := make(map[string]*sums)
	for _, name := range order {
		accum[name] = &sums{}
	}

	for _, row := range rows {
		cs, ok := byCorpus[row.Corpus]
		if !ok {
			cs = newSummary(row.Corpus)
			byCorpus[row.Corpus] = cs
			accum[row.Corpus] = &sums{}
			order = append(order, row.Corpus)
		}
		cs.Total++
		if row.ZeroConfig.Status == statusOK {
			cs.ZeroOK++
		}
		if row.BestConfig.Status == statusOK {
			cs.BestOK++
		}
		if row.ZeroConfig.Status == statusUnknown || row.GroundTruth == "—" {
			cs.ZeroUnknown++
		}
		if row.BestConfig.Status == statusUnknown || row.GroundTruth == "—" {
			cs.BestUnknown++
		}

		// Best-config duration is summed unconditionally (covers all images).
		cs.BestDurationMS += row.BestConfig.DurationMS

		// Failure-mode histogram: exclude unknown/skipped (no ground truth).
		if row.BestConfig.Status != statusUnknown && row.BestConfig.Status != statusSkipped {
			cs.BestFailModes[whyToBucket(row.BestConfig.Status, row.BestConfig.Why)]++
		}

		ac := accum[row.Corpus]
		hasGT := row.ZeroConfig.Score >= 0 // Score < 0 means no ground truth

		if hasGT {
			ac.zeroScore += row.ZeroConfig.Score
			ac.zeroScoreN++
			if row.ZeroConfig.Score >= 70 {
				cs.ZeroSensical++
			}
			ac.zeroConf += row.ZeroConfig.Confidence
			ac.zeroConfN++
			fid := max(0.0, min(1.0, 1-row.ZeroConfig.BestTotal))
			ac.zeroFid += fid
			ac.zeroFidN++
		}
		if row.BestConfig.Score >= 0 {
			ac.bestScore += row.BestConfig.Score
			ac.bestScoreN++
			if row.BestConfig.Score >= 70 {
				cs.BestSensical++
			}
			ac.bestConf += row.BestConfig.Confidence
			ac.bestConfN++
			fid := max(0.0, min(1.0, 1-row.BestConfig.BestTotal))
			ac.bestFid += fid
			ac.bestFidN++
		}
	}

	// Finalise means.
	result := make([]journalCorpusSummary, 0, len(order))
	for _, name := range order {
		cs, ok := byCorpus[name]
		if !ok || cs.Total == 0 {
			continue
		}
		ac := accum[name]
		if ac.zeroScoreN > 0 {
			cs.ZeroMeanScore = ac.zeroScore / float64(ac.zeroScoreN)
		}
		if ac.bestScoreN > 0 {
			cs.BestMeanScore = ac.bestScore / float64(ac.bestScoreN)
		}
		if ac.zeroConfN > 0 {
			cs.ZeroMeanConf = ac.zeroConf / float64(ac.zeroConfN)
		}
		if ac.bestConfN > 0 {
			cs.BestMeanConf = ac.bestConf / float64(ac.bestConfN)
		}
		if ac.zeroFidN > 0 {
			cs.ZeroMeanFidelity = ac.zeroFid / float64(ac.zeroFidN)
		}
		if ac.bestFidN > 0 {
			cs.BestMeanFidelity = ac.bestFid / float64(ac.bestFidN)
		}
		result = append(result, *cs)
	}
	return result
}
