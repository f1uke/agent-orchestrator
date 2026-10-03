package rules

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// BM25 parameters, the usual defaults.
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// Doc is one searchable rule.
type Doc struct {
	ID   string
	Text string
}

// Hit is a search result.
type Hit struct {
	ID    string
	Score float64
}

// Index is a BM25 index over a corpus small enough (a few thousand rules) to
// build per query: no embeddings, no state to keep in step with the corpus.
type Index struct {
	docs   []indexed
	df     map[string]int
	avgLen float64
}

type indexed struct {
	id  string
	tf  map[string]int
	len int
}

// NewIndex indexes docs.
func NewIndex(docs []Doc) *Index {
	ix := &Index{df: map[string]int{}}
	total := 0
	for _, d := range docs {
		toks := Tokens(d.Text)
		tf := map[string]int{}
		for _, t := range toks {
			tf[t]++
		}
		for t := range tf {
			ix.df[t]++
		}
		ix.docs = append(ix.docs, indexed{id: d.ID, tf: tf, len: len(toks)})
		total += len(toks)
	}
	if len(docs) > 0 {
		ix.avgLen = float64(total) / float64(len(docs))
	}
	return ix
}

// Search returns at most limit docs that share a term with query, best first;
// ties keep corpus order.
func (ix *Index) Search(query string, limit int) []Hit {
	terms := map[string]bool{}
	for _, t := range Tokens(query) {
		terms[t] = true
	}
	n := float64(len(ix.docs))
	var hits []Hit
	for _, d := range ix.docs {
		score := 0.0
		for t := range terms {
			f := float64(d.tf[t])
			if f == 0 {
				continue
			}
			df := float64(ix.df[t])
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			score += idf * f * (bm25K1 + 1) / (f + bm25K1*(1-bm25B+bm25B*float64(d.len)/ix.avgLen))
		}
		if score > 0 {
			hits = append(hits, Hit{ID: d.id, Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// Tokens lowercases s and splits it into words of letters and digits, drops
// English stop words and folds a plural "s" so "scripts" finds "script".
func Tokens(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.Is(unicode.Mn, r) && !unicode.Is(unicode.Mc, r)
	}) {
		if len([]rune(w)) < 2 || stopWords[w] {
			continue
		}
		if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
			w = strings.TrimSuffix(w, "s")
		}
		out = append(out, w)
	}
	return out
}

var stopWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`a an and are as at be but by can do does for from has have if in into is it its
		of on or so than that the their them then there these they this those to was were when where which while
		who will with you your not no any all also only just more most must should may might it's don't`) {
		m[w] = true
	}
	return m
}()
