package utils

import (
	"reflect"
	"testing"
)

func TestBM25Analyzer_Stopwords(t *testing.T) {
	got := bm25Analyzer{stopwords: true}.tokenize("Create an issue in the repository")
	want := []string{"create", "issue", "repository"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize() = %v, want %v", got, want)
	}

	// Stopwords are dropped before stemming, so "was" is not stemmed into a
	// stray "wa" token.
	got = bm25Analyzer{stemming: true, stopwords: true}.tokenize("It was creating issues")
	want = []string{"creat", "issu"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize() with stemming = %v, want %v", got, want)
	}

	// Words that can still discriminate between tools stay searchable.
	kept := []string{"all", "not", "only", "up", "out", "after", "before", "may", "go", "new"}
	got = bm25Analyzer{stopwords: true}.tokenize("all not only up out after before may go new")
	if !reflect.DeepEqual(got, kept) {
		t.Fatalf("tokenize() = %v, want %v", got, kept)
	}

	// Off by default.
	if got := (bm25Analyzer{}).tokenize("create an issue"); len(got) != 3 {
		t.Fatalf("tokenize() without stopwords = %v, want 3 tokens", got)
	}
}

func TestBM25Search_WithEnglishStopwords(t *testing.T) {
	corpus := []testDoc{
		{1, "add a comment to an existing issue"},
		{2, "create a new issue in a repository"},
		{3, "list issues in a repository"},
		{4, "create a repository"},
	}
	query := "creating an issue"

	// "an" appears in a single document, so without stopword removal that
	// filler word has the highest idf in the query and ranks doc 1 first.
	plain := NewBM25Engine(corpus, extractText, WithStemming())
	if got := plain.Search(query, 4)[0].Document.ID; got != 1 {
		t.Fatalf("without stopwords top = %d, want 1 (documents the failure mode)", got)
	}

	engine := NewBM25Engine(corpus, extractText, WithStemming(), WithEnglishStopwords())
	if got := engine.Search(query, 4)[0].Document.ID; got != 2 {
		t.Fatalf("with stopwords top = %d, want 2", got)
	}

	// A query made only of stopwords has nothing left to match.
	if got := engine.Search("what is this", 4); len(got) != 0 {
		t.Fatalf("stopword-only query returned %d results, want 0", len(got))
	}
}
