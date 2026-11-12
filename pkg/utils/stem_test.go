package utils

import (
	"reflect"
	"strings"
	"testing"
)

func TestStemEnglish_Inflections(t *testing.T) {
	tests := []struct {
		base       string
		inflection string
	}{
		{"search", "searching"},
		{"search", "searches"},
		{"search", "searched"},
		{"create", "creating"},
		{"create", "created"},
		{"create", "creates"},
		{"update", "updating"},
		{"delete", "deleting"},
		{"merge", "merging"},
		{"write", "writing"},
		{"use", "using"},
		{"list", "listing"},
		{"get", "getting"},
		{"run", "running"},
		{"set", "setting"},
		{"query", "querying"},
		{"query", "queries"},
		{"repository", "repositories"},
		{"policy", "policies"},
		{"status", "statuses"},
		{"class", "classes"},
		{"process", "processes"},
		{"copy", "copying"},
		{"apply", "applying"},
		{"reply", "replying"},
		{"read", "reading"},
		{"send", "sending"},
		{"manage", "managing"},
		{"configure", "configuring"},
		{"execute", "executing"},
	}

	for _, tt := range tests {
		t.Run(tt.base+"_"+tt.inflection, func(t *testing.T) {
			base := stemEnglish(tt.base)
			inflected := stemEnglish(tt.inflection)
			if base != inflected {
				t.Errorf("stemEnglish(%q) = %q, stemEnglish(%q) = %q; want equal stems",
					tt.base, base, tt.inflection, inflected)
			}
		})
	}
}

func TestStemEnglish_LeavesForeignTokensAlone(t *testing.T) {
	tokens := []string{
		"",
		"is",
		"us",
		"go",
		"café",
		"检索",
		"abc123",
		"mcp_github_search_repositories",
		"API",
		strings.Repeat("y", maxStemTokenLen+1),
	}

	for _, token := range tokens {
		if got := stemEnglish(token); got != token {
			t.Errorf("stemEnglish(%q) = %q, want unchanged", token, got)
		}
	}
}

func TestBM25Analyzer_Stemming(t *testing.T) {
	got := bm25Analyzer{stemming: true}.tokenize("Searching repositories, created issues!")
	want := []string{"search", "repositori", "creat", "issu"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize() with stemming = %v, want %v", got, want)
	}

	got = bm25Analyzer{}.tokenize("Searching repositories")
	want = []string{"searching", "repositories"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize() without stemming = %v, want %v", got, want)
	}
}

func TestBM25Search_WithStemming(t *testing.T) {
	corpus := []testDoc{
		{1, "create issue"},
		{2, "search repositories"},
	}

	engine := NewBM25Engine(corpus, extractText, WithStemming())

	for _, query := range []string{"creating issues", "searching repository"} {
		results := engine.Search(query, 5)
		if len(results) != 1 {
			t.Errorf("Search(%q) returned %d results, want 1", query, len(results))
		}
	}

	// Without the option the engine keeps the original tokenization, so an
	// inflected query must not match the base-form corpus.
	plain := NewBM25Engine(corpus, extractText)
	if results := plain.Search("creating issues", 5); len(results) != 0 {
		t.Errorf("Search without stemming returned %d results, want 0", len(results))
	}
}
