package memory

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// BM25 parameters: the textbook defaults. The corpus is dozens of short
// documents and there is nothing to tune against yet.
const (
	k1 = 1.2
	b  = 0.75

	// A memory named for its subject should win a query about it even when
	// the body never repeats the phrase.
	nameWeight        = 3
	descriptionWeight = 2
	bodyWeight        = 1
)

// Match is one ranked memory.
type Match struct {
	Memory
	Score float64
}

// Rank returns the memories a query is about, best first, at most limit of
// them. No model and no embeddings: BM25 is deterministic and needs neither.
func Rank(query string, corpus []Memory, limit int) []Match {
	terms := tokenize(query)
	if len(terms) == 0 || len(corpus) == 0 {
		return nil
	}

	docs := make([]map[string]int, len(corpus))
	lengths := make([]float64, len(corpus))
	var total float64
	for i, m := range corpus {
		docs[i] = termFrequencies(m)
		for _, n := range docs[i] {
			lengths[i] += float64(n)
		}
		total += lengths[i]
	}
	avgLen := total / float64(len(corpus))
	if avgLen == 0 {
		return nil
	}

	var out []Match
	for i, tf := range docs {
		var score float64
		for _, term := range terms {
			f := float64(tf[term])
			if f == 0 {
				continue
			}
			norm := f * (k1 + 1) / (f + k1*(1-b+b*lengths[i]/avgLen))
			score += idf(term, docs) * norm
		}
		if score > 0 {
			out = append(out, Match{Memory: corpus[i], Score: score})
		}
	}

	// Equal scores keep a deterministic order, so the same query never
	// returns the same memories shuffled.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Name < out[j].Name
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// idf is the smoothed inverse document frequency. A term in every document
// scores the same everywhere and so separates nothing.
func idf(term string, docs []map[string]int) float64 {
	var df float64
	for _, tf := range docs {
		if tf[term] > 0 {
			df++
		}
	}
	n := float64(len(docs))
	return math.Log(1 + (n-df+0.5)/(df+0.5))
}

// termFrequencies counts a memory's terms with its fields weighted.
func termFrequencies(m Memory) map[string]int {
	tf := map[string]int{}
	add := func(text string, weight int) {
		for _, t := range tokenize(text) {
			tf[t] += weight
		}
	}
	add(m.Name, nameWeight)
	add(m.Description, descriptionWeight)
	add(m.Body, bodyWeight)
	return tf
}

// tokenize lowercases and splits on anything that is not a letter or a digit,
// which is what makes `docker-compose` match `docker compose`. No stemming and
// no stop-word list: a corpus this small pays nothing for either.
func tokenize(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return fields
}
