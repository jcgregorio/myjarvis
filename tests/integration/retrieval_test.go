//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
	"github.com/jcgregorio/myjarvis/internal/llm"
	"github.com/jcgregorio/myjarvis/internal/rag"
)

// retrieval_test.go investigates RAG retrieval ranking quality, lever #1:
// does feeding the raw user question to the sidecar retrieve the correct
// canonical Wikipedia article better than the keyword query the LLM
// router generates?
//
// For each case it (a) runs the real router to capture the
// search_wikipedia "query" arg, then probes the sidecar three ways —
// the router's keyword query, the raw question, and both concatenated —
// recording the rank at which the expected article title first appears
// (0 = not in top-K). Skipped unless OLLAMA_URL is set.
//
// Run: make test-retrieval

type retrievalCase struct {
	question string
	// expected canonical article title (case-insensitive, anchored-ish)
	want *regexp.Regexp
}

func retrievalCases() []retrievalCase {
	ci := func(s string) *regexp.Regexp { return regexp.MustCompile("(?i)" + s) }
	return []retrievalCase{
		{"How big is the moon?", ci(`^moon$`)},
		{"Who invented the transistor?", ci(`^(history of the )?transistor$`)},
		{"How tall is Mount Everest?", ci(`^mount everest$`)},
		{"What is the speed of light?", ci(`^speed of light$`)},
		{"When did World War 2 end?", ci(`world war ii|end of world war`)},
		{"Who wrote Pride and Prejudice?", ci(`^pride and prejudice$`)},
		{"What is the boiling point of water?", ci(`^(boiling point|water)$`)},
		{"What is the capital of France?", ci(`^(paris|france)$`)},
		{"What is photosynthesis?", ci(`^photosynthesis$`)},
		{"Who painted the Mona Lisa?", ci(`^(mona lisa|leonardo da vinci)$`)},
		{"How far is the Sun from the Earth?", ci(`^(astronomical unit|earth's orbit|sun)$`)},
		{"What is the tallest mountain in the solar system?", ci(`^(olympus mons|list of tallest mountains)`)},
	}
}

// payloadString safely reads a string field from a hit payload.
func payloadString(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok { return v }
	return ""
}

const retrievalK = 10

// rankOf returns the 1-based rank of the first hit whose title matches
// want, or 0 if not present in the slice.
func rankOf(hits []rag.Hit, want *regexp.Regexp) (int, string) {
	for i, h := range hits {
		title := payloadString(h.Payload, "title")
		if want.MatchString(strings.TrimSpace(title)) {
			return i + 1, title
		}
	}
	top := ""
	if len(hits) > 0 {
		top = payloadString(hits[0].Payload, "title")
	}
	return 0, top
}

func TestRetrievalProbe(t *testing.T) {
	baseURL := os.Getenv("OLLAMA_URL")
	if baseURL == "" {
		t.Skip("OLLAMA_URL not set — skipping (run via `make test-retrieval`)")
	}
	model := os.Getenv("MODEL")
	if model == "" {
		model = "granite4:latest"
	}
	ragURL := os.Getenv("RAG_URL")
	if ragURL == "" {
		ragURL = "http://goldmine-prime:8011"
	}

	lc := llm.NewClient(baseURL, model)
	rag := rag.NewClient(ragURL)
	tools := llm.BuildTools(fixtureEntities(), fixtureLists, nil)

	type tally struct{ hit, sumRank, n int }
	var byQuery, byQuestion, byBoth tally
	score := func(tl *tally, rank int) {
		tl.n++
		if rank > 0 && rank <= 5 {
			tl.hit++
			tl.sumRank += rank
		}
	}

	for _, c := range retrievalCases() {
		c := c
		t.Run(c.question, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			// Capture the router's real keyword query.
			calls, _, err := lc.Chat(ctx, c.question, tools)
			if err != nil {
				t.Fatalf("router error: %v", err)
			}
			kw := ""
			for _, tc := range calls {
				if tc.Name == "search_wikipedia" {
					var a struct {
						Query string `json:"query"`
					}
					_ = json.Unmarshal([]byte(tc.Args), &a)
					kw = a.Query
				}
			}
			if kw == "" {
				kw = c.question // router didn't route to wiki; degrade gracefully
			}

			qHits, _ := rag.Search(ctx, "WIKIPEDIA_ENGLISH", kw, retrievalK)
			nHits, _ := rag.Search(ctx, "WIKIPEDIA_ENGLISH", c.question, retrievalK)
			bHits, _ := rag.Search(ctx, "WIKIPEDIA_ENGLISH", kw+" "+c.question, retrievalK)

			qr, qTop := rankOf(qHits, c.want)
			nr, nTop := rankOf(nHits, c.want)
			br, _ := rankOf(bHits, c.want)
			score(&byQuery, qr)
			score(&byQuestion, nr)
			score(&byBoth, br)

			fmt.Printf("RETR | %-45s | kw=%-32q | query:rank=%-2d question:rank=%-2d both:rank=%-2d | qTop=%q nTop=%q\n",
				truncate(c.question, 45), truncate(kw, 32), qr, nr, br, truncate(qTop, 28), truncate(nTop, 28))
		})
	}

	line := func(name string, tl tally) {
		avg := 0.0
		if tl.hit > 0 {
			avg = float64(tl.sumRank) / float64(tl.hit)
		}
		fmt.Printf("RETR-SUMMARY | %-10s hit@5=%d/%d  mean_rank_when_hit=%.2f\n",
			name, tl.hit, tl.n, avg)
	}
	fmt.Println("==================== RETRIEVAL PROBE ====================")
	line("query", byQuery)
	line("question", byQuestion)
	line("both", byBoth)
	fmt.Println("=========================================================")
}
