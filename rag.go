package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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

const ragLimit = 5

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
			"Give answers suitable for text-to-speech — no markdown, no lists, no special formatting. "+
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

	hits, err := s.rag.Search(ctx, "WIKIPEDIA_ENGLISH", q, ragLimit)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "I couldn't find a Wikipedia article that answers that.", nil
	}

	log.Printf("[rag] wiki: %d hits for %q", len(hits), q)
	var b strings.Builder
	for i, h := range hits {
		title := payloadString(h.Payload, "title")
		url := payloadString(h.Payload, "url")
		content := payloadString(h.Payload, "content")
		log.Printf("[rag]   #%d %.3f %s", i+1, h.Score, title)
		fmt.Fprintf(&b, "--- Wikipedia: %s (%s) ---\n%s\n\n", title, url, content)
	}

	question := p.effectiveQuestion()
	brevity := "Keep your answer short — just the key fact or facts."
	if wantsVerbose(question) {
		brevity = "Give a thorough answer with relevant details."
	}

	return s.llm.ChatPlain(ctx,
		"You are a helpful home assistant. Answer questions using the provided Wikipedia articles. "+
			"Give answers suitable for text-to-speech — no markdown, no lists, no special formatting. "+
			"Briefly mention which Wikipedia article you drew the answer from "+
			`(e.g. "according to the Wikipedia article on Transistors"). `+
			brevity,
		fmt.Sprintf("Here are some Wikipedia articles:\n\n%s\nQuestion: %s", b.String(), question),
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
