//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
	"github.com/jcgregorio/myjarvis/internal/llm"
	"github.com/jcgregorio/myjarvis/internal/rag"
)

// synthesis_test.go exercises the *second* LLM call in the RAG path —
// answer synthesis (rag.Searcher.AnswerFromWikipedia -> ChatPlain over
// retrieved chunks). Routing accuracy (routing_test.go) says nothing
// about whether a model writes a good spoken answer from context; this
// does. Scoring is deterministic and reproducible:
//
//   - facts:   answer contains the expected fact for the question
//   - clean:   no markdown artifacts (bad for TTS)
//   - attrib:  answer cites Wikipedia (the production prompt asks for it)
//   - nonempty
//
// Retrieval (the RAG sidecar / Qdrant) is held constant across models,
// so differences are the synthesis model. Skipped unless OLLAMA_URL is
// set. Run via `make test-synth` or scripts/bench-synth.sh.
//
// Every answer is appended verbatim to $SYNTH_ANSWERS_FILE (if set) so
// the prose can be read qualitatively for the top candidates.

type synthCase struct {
	query    string
	question string
	fact     *regexp.Regexp // case-insensitive; a correct answer matches
}

func synthCases() []synthCase {
	ci := func(s string) *regexp.Regexp { return regexp.MustCompile("(?i)" + s) }
	return []synthCase{
		{"transistor invention", "Who invented the transistor?",
			ci(`bardeen|brattain|shockley|bell lab`)},
		{"Mount Everest height", "How tall is Mount Everest?",
			ci(`8[,. ]?8(4[89]|50)|29[,. ]?0(29|3[12])`)},
		{"speed of light", "What is the speed of light?",
			ci(`299[,.\s]?792|186[,.]?282|3\s*[×x*]\s*10`)},
		{"end of World War II", "When did World War 2 end?",
			ci(`1945`)},
		{"Pride and Prejudice author", "Who wrote Pride and Prejudice?",
			ci(`austen`)},
		{"boiling point of water", "What is the boiling point of water in Fahrenheit?",
			ci(`\b212\b`)},
		// Adversarial: these retrieve disambiguation pages first, so they
		// exercise the re-rank + adversarial re-query path.
		{"Eiffel Tower height", "How tall is the Eiffel Tower?",
			ci(`3(0[0-9]|1[0-9]|2[0-9])\s*(m|metre|meter)|1,?0\d\d\s*(ft|feet)`)},
		{"capital of France", "What is the capital of France?",
			ci(`paris`)},
	}
}

// markdown / non-TTS artifacts: asterisks, hashes, backticks, or list
// markers at the start of a line.
var ttsDirty = regexp.MustCompile("[*#`]|(?m)^\\s*[-•]\\s|(?m)^\\s*\\d+\\.\\s")

type synthResult struct {
	question string
	ok       bool // fact present
	clean    bool
	attrib   bool
	nonempty bool
	latency  time.Duration
	answer   string
}

func TestSynthesis(t *testing.T) {
	baseURL := os.Getenv("OLLAMA_URL")
	if baseURL == "" {
		t.Skip("OLLAMA_URL not set — skipping live synthesis test (run via `make test-synth`)")
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
	searcher := rag.NewSearcher(rag.NewClient(ragURL), lc)

	// Warm the model (model switches between sweep runs are cold).
	{
		wctx, wcancel := context.WithTimeout(context.Background(), 180*time.Second)
		_, _ = searcher.AnswerFromWikipedia(wctx,
			`{"query":"warmup","question":"what is water"}`)
		wcancel()
	}

	var answersOut *os.File
	if p := os.Getenv("SYNTH_ANSWERS_FILE"); p != "" {
		answersOut, _ = os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if answersOut != nil {
			defer answersOut.Close()
		}
	}

	cases := synthCases()
	t.Logf("synthesis test: model=%s rag=%s cases=%d", model, ragURL, len(cases))

	var results []synthResult
	for _, c := range cases {
		c := c
		t.Run(c.question, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			args := fmt.Sprintf(`{"query":%q,"question":%q}`, c.query, c.question)
			start := time.Now()
			ans, err := searcher.AnswerFromWikipedia(ctx, args)
			elapsed := time.Since(start)
			if err != nil {
				t.Fatalf("AnswerFromWikipedia error after %s: %v",
					elapsed.Round(time.Millisecond), err)
			}

			r := synthResult{
				question: c.question,
				nonempty: strings.TrimSpace(ans) != "",
				ok:       c.fact.MatchString(ans),
				clean:    !ttsDirty.MatchString(ans),
				attrib:   strings.Contains(strings.ToLower(ans), "wikipedia"),
				latency:  elapsed,
				answer:   ans,
			}
			results = append(results, r)

			if answersOut != nil {
				fmt.Fprintf(answersOut,
					"\n## [%s] %s  (%s, fact=%v clean=%v attrib=%v)\n%s\n",
					model, c.question, elapsed.Round(time.Millisecond),
					r.ok, r.clean, r.attrib, strings.TrimSpace(ans))
			}

			status := "ok"
			if !r.ok {
				status = "FACT-MISS"
			}
			t.Logf("%s in %s | clean=%v attrib=%v | %s",
				status, elapsed.Round(time.Millisecond), r.clean, r.attrib,
				truncate(strings.ReplaceAll(ans, "\n", " "), 100))
			if !r.ok {
				t.Errorf("expected fact /%s/ not found in answer", c.fact)
			}
			// Adversarial: the answer must never be sourced from a
			// disambiguation page — re-rank + re-query should prevent it.
			if strings.Contains(strings.ToLower(ans), "disambiguation") {
				t.Errorf("answer references a disambiguation page: %q", truncate(ans, 120))
			}
		})
	}

	synthReport(t, results, model)
}

func synthReport(t *testing.T, rs []synthResult, model string) {
	if len(rs) == 0 {
		return
	}
	n := len(rs)
	var facts, clean, attrib, nonempty int
	lats := make([]time.Duration, 0, n)
	for _, r := range rs {
		if r.ok {
			facts++
		}
		if r.clean {
			clean++
		}
		if r.attrib {
			attrib++
		}
		if r.nonempty {
			nonempty++
		}
		lats = append(lats, r.latency)
	}
	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
	pct := func(x int) float64 { return 100 * float64(x) / float64(n) }

	t.Log("")
	t.Log("=================== SYNTHESIS SUMMARY ===================")
	t.Logf("%-22s facts=%3.0f%% clean=%3.0f%% attrib=%3.0f%% nonempty=%3.0f%%  p50=%s mean=%s",
		model, pct(facts), pct(clean), pct(attrib), pct(nonempty),
		rnd(pctile(lats, 50)), rnd(meanDur(lats)))
	t.Log("=========================================================")

	ms := func(d time.Duration) int64 { return d.Milliseconds() }
	// Machine-readable line for scripts/bench-synth.sh.
	// model,cases,facts%,clean%,attrib%,nonempty%,p50_ms,mean_ms
	t.Logf("SYNTH_CSV:%s,%d,%.0f,%.0f,%.0f,%.0f,%d,%d",
		model, n, pct(facts), pct(clean), pct(attrib), pct(nonempty),
		ms(pctile(lats, 50)), ms(meanDur(lats)))
}
