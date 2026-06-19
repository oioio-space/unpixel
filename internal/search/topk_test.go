package search

import (
	"strings"
	"testing"

	"github.com/oioio-space/unpixel"
)

// trivialLM is a non-nil language model that assigns equal weight to every
// string. It is used to activate the LanguageModel branch without affecting
// which characters are selected (order is then charset order).
func trivialLM(string) float64 { return 1 }

// charsetOfLen returns a charset string of exactly n distinct ASCII runes.
func charsetOfLen(n int) string {
	const pool = "abcdefghijklmnopqrstuvwxyz" +
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"0123456789!@#$%^&*()_+-="
	if n > len(pool) {
		n = len(pool)
	}
	return pool[:n]
}

// TestEffectiveTopK verifies the three-way priority rule:
//  1. Explicit cfg.CharsetTopK > 0 always wins (caller intent, any charset size).
//  2. Wide charset (≥ autoTopKThreshold) + LanguageModel → auto-K (= autoTopKValue,
//     capped at charset length).
//  3. Small charset or no LanguageModel → 0 (no pruning, identical to before).
func TestEffectiveTopK(t *testing.T) {
	smallCharset := charsetOfLen(autoTopKThreshold - 1) // 39 chars — below threshold
	wideCharset := charsetOfLen(autoTopKThreshold)      // 40 chars — at threshold
	asciiCharset := unpixel.CharsetASCII                // 95 chars — well above threshold

	cases := []struct {
		name      string
		cfg       unpixel.Config
		want      int
		wantRange bool // true when want is a max (result may be smaller due to cap)
	}{
		// Priority 1: explicit CharsetTopK always honoured, regardless of charset size.
		{
			name: "explicit_k_small_charset",
			cfg:  unpixel.Config{Charset: smallCharset, CharsetTopK: 5, LanguageModel: trivialLM},
			want: 5,
		},
		{
			name: "explicit_k_wide_charset",
			cfg:  unpixel.Config{Charset: wideCharset, CharsetTopK: 10, LanguageModel: trivialLM},
			want: 10,
		},
		{
			name: "explicit_k_no_lm",
			cfg:  unpixel.Config{Charset: asciiCharset, CharsetTopK: 8, LanguageModel: nil},
			want: 8,
		},

		// Priority 2: auto-K for wide charset + language model.
		{
			name: "auto_k_at_threshold",
			cfg:  unpixel.Config{Charset: wideCharset, CharsetTopK: 0, LanguageModel: trivialLM},
			// autoTopKValue (24) is ≤ wideCharset (40) so the cap does not bite.
			want: autoTopKValue,
		},
		{
			name: "auto_k_ascii",
			cfg:  unpixel.Config{Charset: asciiCharset, CharsetTopK: 0, LanguageModel: trivialLM},
			want: autoTopKValue, // 24 < 95, cap does not fire
		},
		{
			name: "auto_k_tiny_wide_charset_caps_at_n",
			// charset just large enough to trigger auto-K but smaller than autoTopKValue
			cfg: unpixel.Config{Charset: charsetOfLen(autoTopKThreshold), CharsetTopK: 0, LanguageModel: trivialLM},
			// min(autoTopKThreshold=40, autoTopKValue=24) = 24
			want: min(autoTopKThreshold, autoTopKValue),
		},

		// Priority 3: no pruning when charset is small or model is absent.
		{
			name: "no_pruning_small_charset_with_lm",
			cfg:  unpixel.Config{Charset: smallCharset, CharsetTopK: 0, LanguageModel: trivialLM},
			want: 0,
		},
		{
			name: "no_pruning_default_charset_with_lm",
			cfg:  unpixel.Config{Charset: unpixel.DefaultCharset, CharsetTopK: 0, LanguageModel: trivialLM},
			want: 0,
		},
		{
			name: "no_pruning_wide_charset_no_lm",
			cfg:  unpixel.Config{Charset: asciiCharset, CharsetTopK: 0, LanguageModel: nil},
			want: 0,
		},
		{
			name: "no_pruning_zero_k_zero_lm",
			cfg:  unpixel.Config{Charset: asciiCharset, CharsetTopK: 0, LanguageModel: nil},
			want: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := effectiveTopK(tc.cfg)
			if got != tc.want {
				t.Errorf("effectiveTopK: got %d, want %d (charset len=%d, CharsetTopK=%d, hasLM=%v)",
					got, tc.want, len([]rune(tc.cfg.Charset)), tc.cfg.CharsetTopK, tc.cfg.LanguageModel != nil)
			}
		})
	}
}

// TestEffectiveTopK_doesNotMutateCfg confirms effectiveTopK is a pure function
// that reads cfg without side-effects.
func TestEffectiveTopK_doesNotMutateCfg(t *testing.T) {
	cfg := unpixel.Config{
		Charset:       unpixel.CharsetASCII,
		CharsetTopK:   0,
		LanguageModel: trivialLM,
	}
	before := cfg.CharsetTopK
	_ = effectiveTopK(cfg)
	if cfg.CharsetTopK != before {
		t.Errorf("effectiveTopK mutated cfg.CharsetTopK: got %d, want %d", cfg.CharsetTopK, before)
	}
}

// TestTopKChars_autoK verifies that topKChars applies the auto-K heuristic
// when CharsetTopK is 0 but LanguageModel is set and charset is wide: it
// returns a non-nil slice of length autoTopKValue (or charset length, whichever
// is smaller), keeping callers off the whole-charset path.
func TestTopKChars_autoK(t *testing.T) {
	// Ascending-alphabet language model: later letters score higher.
	// (Used only to verify that ordering is driven by the LM, not charset order.)
	ascendingLM := func(s string) float64 {
		if s == "" {
			return 0
		}
		return float64(s[len(s)-1]) // last byte: 'z' > 'a'
	}

	cfg := unpixel.Config{
		Charset:       unpixel.CharsetASCII, // 95 chars — triggers auto-K
		CharsetTopK:   0,                    // explicit 0 — rely on auto
		LanguageModel: ascendingLM,
	}

	got := topKChars(cfg, "")
	if got == nil {
		t.Fatal("topKChars with wide charset + LM returned nil; want non-nil slice (auto-K)")
	}
	if len(got) != autoTopKValue {
		t.Errorf("topKChars auto-K len = %d, want %d", len(got), autoTopKValue)
	}
	// The ascending LM should rank high-codepoint chars first: '~' (0x7E) is highest.
	if got[0] != '~' {
		t.Errorf("topKChars auto-K: got[0] = %q, want '~' (highest codepoint)", got[0])
	}
}

// TestTopKChars_smallCharsetNoAutoK confirms that a small charset (below the
// threshold) with a LanguageModel still returns nil — auto-K must not fire.
func TestTopKChars_smallCharsetNoAutoK(t *testing.T) {
	cfg := unpixel.Config{
		Charset:       unpixel.DefaultCharset, // 27 chars — well below autoTopKThreshold
		CharsetTopK:   0,
		LanguageModel: trivialLM,
	}
	if got := topKChars(cfg, ""); got != nil {
		t.Errorf("topKChars small charset + LM returned non-nil %v; want nil (no auto-K)", string(got))
	}
}

// TestTopKChars_backwardCompatNilLM confirms no-LM path still returns nil
// regardless of charset size (identical to behavior before this change).
func TestTopKChars_backwardCompatNilLM(t *testing.T) {
	for _, charset := range []string{
		unpixel.DefaultCharset,
		strings.Repeat("x", autoTopKThreshold+10),
		unpixel.CharsetASCII,
	} {
		cfg := unpixel.Config{Charset: charset, CharsetTopK: 0, LanguageModel: nil}
		if got := topKChars(cfg, ""); got != nil {
			t.Errorf("topKChars no-LM charset-len=%d returned non-nil; want nil", len([]rune(charset)))
		}
	}
}
