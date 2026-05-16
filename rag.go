package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// RAGClient talks to the goldmine-prime search-server sidecar over HTTP.
// The sidecar embeds the query with SPLADE + BM25 and runs a hybrid
// (DBSF) query against the named Qdrant collection.
type RAGClient struct {
	baseURL string
	http    *http.Client
}

func NewRAGClient(baseURL string) *RAGClient {
	return &RAGClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

type RAGHit struct {
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload"`
}

func (r *RAGClient) Search(ctx context.Context, collection, query string, limit int) ([]RAGHit, error) {
	body, err := json.Marshal(map[string]any{
		"collection": collection,
		"query":      query,
		"limit":      limit,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", r.baseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rag search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rag search: status %d", resp.StatusCode)
	}
	var hits []RAGHit
	if err := json.NewDecoder(resp.Body).Decode(&hits); err != nil {
		return nil, fmt.Errorf("rag search decode: %w", err)
	}
	return hits, nil
}

// RAGSearcher wraps a RAGClient + LLMClient to answer questions using
// retrieved Obsidian or Wikipedia chunks. The retrieval result is
// formatted into a context block and fed to ChatPlain along with the
// user's verbatim question — same two-step pattern as the previous
// VaultSearcher, just backed by hybrid Qdrant retrieval.
type RAGSearcher struct {
	rag *RAGClient
	llm *LLMClient
}

func NewRAGSearcher(rag *RAGClient, llm *LLMClient) *RAGSearcher {
	return &RAGSearcher{rag: rag, llm: llm}
}

type searchArgs struct {
	Query    string `json:"query"`
	Question string `json:"question"`
}

func (a searchArgs) effectiveQuery() string {
	if a.Query != "" {
		return a.Query
	}
	return a.Question
}

func (a searchArgs) effectiveQuestion() string {
	if a.Question != "" {
		return a.Question
	}
	return a.Query
}

const ragLimit = 5  // excerpts fed to synthesis
const ragFetch = 12 // candidates fetched before re-ranking/filtering

var disambigRe = regexp.MustCompile(`(?i)\(disambiguation\)\s*$`)
var indexyRe = regexp.MustCompile(`(?i)^(list|index|outline) of `)

// reRankWikiHits drops Wikipedia disambiguation pages (navigation only —
// no prose to synthesize) and demotes "List/Index/Outline of …" pages
// below real articles, preserving relative order otherwise. topWasJunk
// reports whether the original #1 hit was a disambiguation/index page —
// the signal that triggers an adversarial re-query.
func reRankWikiHits(hits []RAGHit) (cleaned []RAGHit, topWasJunk bool) {
	var primary, secondary []RAGHit
	for i, h := range hits {
		title := strings.TrimSpace(payloadString(h.Payload, "title"))
		switch {
		case disambigRe.MatchString(title):
			if i == 0 {
				topWasJunk = true
			}
			// dropped entirely — a disambiguation page has no content
		case indexyRe.MatchString(title):
			if i == 0 {
				topWasJunk = true
			}
			secondary = append(secondary, h)
		default:
			primary = append(primary, h)
		}
	}
	return append(primary, secondary...), topWasJunk
}

// reformulateQuery asks the LLM for a single better Wikipedia article
// title when the first retrieval surfaced mostly disambiguation/index
// pages. Returns "" on any failure (caller falls back to first results).
func (s *RAGSearcher) reformulateQuery(ctx context.Context, question string, hits []RAGHit) string {
	var titles []string
	for i, h := range hits {
		if i >= 6 {
			break
		}
		if t := strings.TrimSpace(payloadString(h.Payload, "title")); t != "" {
			titles = append(titles, t)
		}
	}
	out, err := s.llm.ChatPlain(ctx,
		"You generate Wikipedia search queries. Reply with ONLY a short search "+
			"phrase naming the single most likely Wikipedia article title that "+
			"answers the question. No explanation, no quotes.",
		fmt.Sprintf("Question: %s\nA prior search returned mostly disambiguation "+
			"or list pages: %s\nGive the best specific Wikipedia article title to search for.",
			question, strings.Join(titles, "; ")),
	)
	if err != nil {
		log.Printf("[rag] reformulate error: %v", err)
		return ""
	}
	out = strings.TrimSpace(out)
	if out == "" || len(out) > 80 { // guard against a rambling reply
		return ""
	}
	return out
}

func (s *RAGSearcher) AnswerFromNotes(ctx context.Context, args string) (string, error) {
	var p searchArgs
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	q := p.effectiveQuery()
	if q == "" {
		return "", fmt.Errorf("empty query")
	}

	hits, err := s.rag.Search(ctx, "obsidian_vault", q, ragLimit)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "I couldn't find any notes matching that.", nil
	}

	log.Printf("[rag] notes: %d hits for %q", len(hits), q)
	var b strings.Builder
	for i, h := range hits {
		path := payloadString(h.Payload, "path")
		heading := payloadString(h.Payload, "heading")
		content := payloadString(h.Payload, "content")
		log.Printf("[rag]   #%d %.3f %s :: %s", i+1, h.Score, path, heading)
		fmt.Fprintf(&b, "--- File: %s (section: %s) ---\n%s\n\n", path, heading, content)
	}

	question := p.effectiveQuestion()
	brevity := "Keep your answer short — just the key fact or facts."
	if wantsVerbose(question) {
		brevity = "Give a thorough answer with relevant details."
	}

	return s.llm.ChatPlain(ctx,
		"You are a helpful home assistant. Answer questions using the provided documents. "+
			"Give answers suitable for text-to-speech — no markdown, no lists, no special formatting; "+
			"write measurements and symbols as spoken words (say 'feet' not 'ft', 'percent' not '%') "+
			"and avoid parenthetical unit conversions. "+
			brevity,
		fmt.Sprintf("Here are some documents from my notes:\n\n%s\nQuestion: %s", b.String(), question),
	)
}

func (s *RAGSearcher) AnswerFromWikipedia(ctx context.Context, args string) (string, error) {
	var p searchArgs
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	q := p.effectiveQuery()
	if q == "" {
		return "", fmt.Errorf("empty query")
	}
	// Retrieve on the keyword query AND the raw question. Benchmarked
	// (retrieval_test.go) at 10/12 expected-article recall vs 8/12 for
	// the keyword query alone, with the best mean rank — the LLM's
	// keyword distillation sometimes drops the term that surfaces the
	// canonical article (e.g. "Mount Everest" kw → rank 7; question → 2).
	retr := q
	if qn := p.effectiveQuestion(); qn != "" && qn != q {
		retr = q + " " + qn
	}

	hits, err := s.rag.Search(ctx, "WIKIPEDIA_ENGLISH", retr, ragFetch)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "I couldn't find a Wikipedia article that answers that.", nil
	}

	cleaned, topWasJunk := reRankWikiHits(hits)

	// Adversarial second pass: if the best hit was a disambiguation /
	// index page (or nothing usable survived), ask the LLM for a better
	// article title and retrieve once more. Bounded to a single retry.
	if topWasJunk || len(cleaned) == 0 {
		if alt := s.reformulateQuery(ctx, p.effectiveQuestion(), hits); alt != "" {
			if h2, e2 := s.rag.Search(ctx, "WIKIPEDIA_ENGLISH", alt, ragFetch); e2 == nil && len(h2) > 0 {
				c2, junk2 := reRankWikiHits(h2)
				if len(c2) > 0 && !junk2 {
					log.Printf("[rag] wiki: adversarial re-query %q -> %d clean hits", alt, len(c2))
					cleaned = c2
				} else if len(cleaned) == 0 {
					cleaned = c2
				}
			}
		}
	}
	if len(cleaned) == 0 {
		return "I couldn't find a Wikipedia article that answers that.", nil
	}
	if len(cleaned) > ragLimit {
		cleaned = cleaned[:ragLimit]
	}

	log.Printf("[rag] wiki: %d hits for %q (topWasJunk=%v)", len(cleaned), q, topWasJunk)
	var b strings.Builder
	for i, h := range cleaned {
		title := payloadString(h.Payload, "title")
		url := payloadString(h.Payload, "url")
		content := payloadString(h.Payload, "content")
		log.Printf("[rag]   #%d %.3f %s", i+1, h.Score, title)
		fmt.Fprintf(&b, "--- Wikipedia: %s (%s) ---\n%s\n\n", title, url, content)
	}

	question := p.effectiveQuestion()
	brevity := "Answer in one or two short sentences — the direct answer only, no extra background."
	if wantsVerbose(question) {
		brevity = "Give a thorough answer with relevant details."
	}

	return s.llm.ChatPlain(ctx,
		"You are a home assistant answering a spoken question using the provided Wikipedia excerpts. "+
			"Pick only the single excerpt that actually answers the question and ignore the rest. "+
			`Begin with "According to the Wikipedia article on X, " naming that one article, then give the answer. `+
			"If none of the excerpts actually answer the question, say you don't have that information — "+
			"do not stitch together or pad with unrelated facts. "+
			"Plain text only, for text-to-speech: no markdown, no lists, no special formatting; "+
			"write measurements and symbols as spoken words (say 'feet' not 'ft', "+
			"'degrees Celsius' not '°C', 'percent' not '%') and avoid parenthetical unit conversions. "+
			brevity,
		fmt.Sprintf("Here are some Wikipedia excerpts:\n\n%s\nQuestion: %s", b.String(), question),
	)
}

// verbose phrases that signal the user wants a detailed answer.
var verbosePhrases = []string{"research", "deep research", "explain to me", "explain", "tell me everything", "in detail", "summarize", "summary"}

func wantsVerbose(question string) bool {
	q := strings.ToLower(question)
	for _, phrase := range verbosePhrases {
		if strings.Contains(q, phrase) {
			return true
		}
	}
	return false
}

func payloadString(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}
