// This file implements the lightweight English stemmer used by BM25.

package utils

import "strings"

// maxStemTokenLen bounds the suffix scan. Longer tokens are identifiers or
// non-English text, not words that benefit from English inflection rules.
const maxStemTokenLen = 64

// stemEnglish reduces inflectional English suffixes using Porter step 1
// (plurals, -ed/-ing, final -y) plus the final-e rule from step 5a. It is
// deliberately smaller than a full Porter stemmer: derivational suffixes
// (steps 2-4) are left alone because tool names and descriptions mostly use
// base words and their inflections.
//
// Only lowercase ASCII letter tokens are processed; identifiers with digits
// (for example content hashes in MCP tool names) and non-English words are
// returned as-is. Callers must lowercase the token first.
func stemEnglish(s string) string {
	if len(s) < 3 || len(s) > maxStemTokenLen || !isASCIILower(s) {
		return s
	}
	s = stemStep1a(s)
	s = stemStep1b(s)
	s = stemStep1c(s)
	return stemStep5a(s)
}

func isASCIILower(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return true
}

// stemStep1a handles plurals: caresses -> caress, ponies -> poni,
// cats -> cat.
//
// Unlike Porter, words ending in "us" or "is" keep their s (status, analysis,
// this) so they are not cut to non-words (statu, analysi). Everything else
// ending in s loses it, including short words such as "gas" -> "ga"; that is
// harmless because corpus and query are stemmed the same way.
func stemStep1a(s string) string {
	switch {
	case strings.HasSuffix(s, "sses"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "ies"):
		return s[:len(s)-3] + "i"
	case strings.HasSuffix(s, "ss"), strings.HasSuffix(s, "us"), strings.HasSuffix(s, "is"):
		return s
	case strings.HasSuffix(s, "s"):
		return s[:len(s)-1]
	}
	return s
}

// stemStep1b handles -eed, -ed and -ing, then repairs the stem (adds the
// missing e, collapses a doubled consonant, or restores e after a CVC).
func stemStep1b(s string) string {
	if strings.HasSuffix(s, "eed") {
		if measure(s[:len(s)-3]) > 0 {
			return s[:len(s)-1]
		}
		return s
	}
	if strings.HasSuffix(s, "ed") && containsVowel(s[:len(s)-2]) {
		return stemStep1bStem(s[:len(s)-2])
	}
	if strings.HasSuffix(s, "ing") && containsVowel(s[:len(s)-3]) {
		return stemStep1bStem(s[:len(s)-3])
	}
	return s
}

func stemStep1bStem(s string) string {
	if strings.HasSuffix(s, "at") || strings.HasSuffix(s, "bl") || strings.HasSuffix(s, "iz") {
		return s + "e"
	}
	if len(s) >= 2 && s[len(s)-1] == s[len(s)-2] && isConsonantAt(s, len(s)-1) {
		switch s[len(s)-1] {
		case 'l', 's', 'z':
			// keep the double consonant
		default:
			return s[:len(s)-1]
		}
	}
	if measure(s) == 1 && cvc(s) {
		return s + "e"
	}
	return s
}

// stemStep1c turns a final y into i when the stem contains a vowel:
// happy -> happi, sky -> sky.
func stemStep1c(s string) string {
	if strings.HasSuffix(s, "y") && containsVowel(s[:len(s)-1]) {
		return s[:len(s)-1] + "i"
	}
	return s
}

// stemStep5a removes a final e when the stem is long enough, or when it is a
// short CVC stem. This is what makes create/creating and use/using converge.
func stemStep5a(s string) string {
	if !strings.HasSuffix(s, "e") {
		return s
	}
	stem := s[:len(s)-1]
	m := measure(stem)
	if m > 1 || (m == 1 && !cvc(stem)) {
		return stem
	}
	return s
}

// measure returns the Porter m value: the number of VC sequences in s.
func measure(s string) int {
	m := 0
	i := 0
	for i < len(s) && isConsonantAt(s, i) {
		i++
	}
	for i < len(s) {
		for i < len(s) && !isConsonantAt(s, i) {
			i++
		}
		if i >= len(s) {
			break
		}
		m++
		for i < len(s) && isConsonantAt(s, i) {
			i++
		}
	}
	return m
}

func containsVowel(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isConsonantAt(s, i) {
			return true
		}
	}
	return false
}

func cvc(s string) bool {
	if len(s) < 3 {
		return false
	}
	last := s[len(s)-1]
	if last == 'w' || last == 'x' || last == 'y' {
		return false
	}
	return isConsonantAt(s, len(s)-3) &&
		!isConsonantAt(s, len(s)-2) &&
		isConsonantAt(s, len(s)-1)
}

func isConsonantAt(s string, i int) bool {
	switch s[i] {
	case 'a', 'e', 'i', 'o', 'u':
		return false
	case 'y':
		if i == 0 {
			return true
		}
		return !isConsonantAt(s, i-1)
	default:
		return true
	}
}
