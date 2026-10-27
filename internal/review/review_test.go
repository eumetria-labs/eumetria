package review

import "testing"

func TestSplitLanguages(t *testing.T) {
	for _, c := range []struct{ in, en, de string }{
		{"A good week.\n\n## Nutrition\n- x\n\n=== DEUTSCH ===\n\nEine gute Woche.\n\n## Ernährung\n- x", "A good week.\n\n## Nutrition\n- x", "Eine gute Woche.\n\n## Ernährung\n- x"},
		{"English.\n**=== DEUTSCH ===**\nDeutsch.", "English.", "Deutsch."},
		{"English.\n\n## === DEUTSCH ===\n\nDeutsch.", "English.", "Deutsch."},
		// Without the separator everything stays English.
		{"Only English.", "Only English.", ""},
		// The words alone, inside a sentence, are not the separator.
		{"Say === DEUTSCH === here.", "Say === DEUTSCH === here.", ""},
	} {
		en, de := splitLanguages(c.in)
		if en != c.en || de != c.de {
			t.Errorf("splitLanguages(%q) = %q, %q; want %q, %q", c.in, en, de, c.en, c.de)
		}
	}
}
