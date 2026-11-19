package utils

import "strings"

// englishStopwords are function words with no ranking signal in tool search
// queries or descriptions. The list is deliberately short: articles,
// pronouns, auxiliaries, conjunctions and common prepositions only. Words
// that can still discriminate (all, not, only, up, out, after, may, ...) stay
// searchable.
var englishStopwords = func() map[string]struct{} {
	words := strings.Fields(`
		a an the and or but of to in on at by for with from into about as via
		is are was were be been being it its this that these those
		i me my we our you your he she his her they them their
		if then than so can could should would will do does did has have had
		there here which who whom what when where how why`)
	set := make(map[string]struct{}, len(words))
	for _, w := range words {
		set[w] = struct{}{}
	}
	return set
}()
