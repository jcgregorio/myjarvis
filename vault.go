package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// VaultSearcher searches and summarizes notes in an Obsidian vault.
type VaultSearcher struct {
	vaultDir string
	llm      *LLMClient
}

func NewVaultSearcher(vaultDir string, llm *LLMClient) *VaultSearcher {
	return &VaultSearcher{vaultDir: vaultDir, llm: llm}
}

// SearchNotes greps the vault for keywords, then asks the LLM to answer the
// original question using the matched file contents.
func (v *VaultSearcher) SearchNotes(ctx context.Context, args string) (string, error) {
	var p struct {
		Query    string `json:"query"`
		Question string `json:"question"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}

	docs, err := v.grepVault(p.Query)
	if err != nil {
		return "", err
	}
	if len(docs) == 0 {
		return "I couldn't find any notes matching that query.", nil
	}

	return v.askWithContext(ctx, p.Question, docs)
}

// SummarizeNotes reads the specified note (or searches for it) and asks the
// LLM to summarize it.
func (v *VaultSearcher) SummarizeNotes(ctx context.Context, args string) (string, error) {
	var p struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}

	docs, err := v.grepVault(p.Query)
	if err != nil {
		return "", err
	}
	if len(docs) == 0 {
		return "I couldn't find any notes matching that query.", nil
	}

	return v.askWithContext(ctx, "Summarize the following notes.", docs)
}

const (
	maxDocs        = 5     // send at most this many documents to the LLM
	maxContextKB   = 16384 // hard cap on total context bytes sent to LLM
	smallFileBytes = 4096  // files this size or smaller are sent in full
)

// docMatch holds a scored document with extracted snippets.
type docMatch struct {
	path     string
	score    int
	snippets []string
}

// grepVault searches all .md files in the vault for the space-separated
// keywords in query. Returns the top-scoring documents with relevant snippets
// rather than full file contents. Documents are scored by how many distinct
// keywords they match — more matches = higher relevance.
func (v *VaultSearcher) grepVault(query string) (map[string]string, error) {
	keywords := splitKeywords(query)
	if len(keywords) == 0 {
		return nil, fmt.Errorf("no search keywords provided")
	}
	log.Printf("[vault] search keywords: %v", keywords)

	var scored []docMatch

	err := filepath.Walk(v.vaultDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		// Skip the Lists directory — lists have their own check_list tool
		rel, _ := filepath.Rel(v.vaultDir, path)
		if strings.HasPrefix(rel, "Lists/") || strings.HasPrefix(rel, "Lists\\") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}

		lower := strings.ToLower(string(content))
		lowerName := strings.ToLower(info.Name())

		// Score by number of distinct keywords matched
		score := 0
		for _, kw := range keywords {
			if strings.Contains(lower, kw) || strings.Contains(lowerName, kw) {
				score++
			}
		}
		if score == 0 {
			return nil
		}

		text := string(content)
		var snippets []string
		if len(content) <= smallFileBytes {
			snippets = []string{text}
		} else {
			snippets = extractSections(text, keywords)
		}
		scored = append(scored, docMatch{path: rel, score: score, snippets: snippets})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	log.Printf("[vault] %d matching doc(s):", len(scored))
	for i, dm := range scored {
		log.Printf("[vault]   #%d %q score=%d snippets=%d", i+1, dm.path, dm.score, len(dm.snippets))
	}

	// Take top N docs, respecting the context size cap
	results := make(map[string]string)
	totalBytes := 0
	for i, dm := range scored {
		if i >= maxDocs {
			break
		}
		text := strings.Join(dm.snippets, "\n---\n")
		if totalBytes+len(text) > maxContextKB {
			// Try to fit a truncated version
			remaining := maxContextKB - totalBytes
			if remaining > 200 {
				text = text[:remaining]
			} else {
				break
			}
		}
		results[dm.path] = text
		totalBytes += len(text)
	}

	return results, nil
}

// extractSections splits content on markdown headers (lines starting with #)
// and returns the full text of every section that contains at least one keyword.
// This preserves logical structure (tables, lists) instead of grabbing arbitrary
// lines around a match.
func extractSections(content string, keywords []string) []string {
	lines := strings.Split(content, "\n")

	// Split into sections. A section starts at a # header and runs until the
	// next header or end of file. Content before the first header is section 0.
	type section struct {
		start int
		end   int
	}
	var sections []section
	cur := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") && i > 0 {
			sections = append(sections, section{cur, i})
			cur = i
		}
	}
	sections = append(sections, section{cur, len(lines)})

	// Keep sections that contain at least one keyword match.
	var snippets []string
	for _, sec := range sections {
		block := strings.Join(lines[sec.start:sec.end], "\n")
		lower := strings.ToLower(block)
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				snippets = append(snippets, strings.TrimSpace(block))
				break
			}
		}
	}

	// Fallback: if nothing matched by section (e.g. no headers), return first 40 lines.
	if len(snippets) == 0 {
		end := 40
		if end > len(lines) {
			end = len(lines)
		}
		snippets = append(snippets, strings.Join(lines[:end], "\n"))
	}

	return snippets
}

// verbose phrases that signal the user wants a detailed answer.
var verbosePhrases = []string{"research", "deep research", "explain to me", "explain", "tell me everything", "in detail"}

func wantsVerbose(question string) bool {
	q := strings.ToLower(question)
	for _, phrase := range verbosePhrases {
		if strings.Contains(q, phrase) {
			return true
		}
	}
	return false
}

// askWithContext sends the matched documents plus the question to the LLM
// and returns a natural-language answer suitable for TTS.
func (v *VaultSearcher) askWithContext(ctx context.Context, question string, docs map[string]string) (string, error) {
	var b strings.Builder
	for path, content := range docs {
		fmt.Fprintf(&b, "--- File: %s ---\n%s\n\n", path, content)
	}

	log.Printf("[vault] sending %d doc(s) (%d bytes) to LLM", len(docs), b.Len())
	for path, content := range docs {
		log.Printf("[vault] doc %q (%d bytes):\n%s", path, len(content), content)
	}

	brevity := "Keep your answer short — just the key fact or facts."
	if wantsVerbose(question) {
		brevity = "Give a thorough answer with relevant details."
	}

	return v.llm.ChatPlain(ctx,
		"You are a helpful home assistant. Answer questions using the provided documents. "+
			"Give answers suitable for text-to-speech — no markdown, no lists, no special formatting. "+
			brevity,
		fmt.Sprintf("Here are some documents from my notes:\n\n%s\nQuestion: %s", b.String(), question),
	)
}

var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true,
	"was": true, "were": true, "what": true, "which": true, "who": true,
	"how": true, "does": true, "do": true, "did": true, "has": true,
	"have": true, "had": true, "can": true, "could": true, "will": true,
	"would": true, "should": true, "of": true, "in": true, "on": true,
	"at": true, "to": true, "for": true, "with": true, "from": true,
	"by": true, "about": true, "my": true, "me": true, "i": true,
	"it": true, "its": true, "this": true, "that": true, "and": true,
	"or": true, "but": true, "not": true, "be": true, "been": true,
	"tell": true, "find": true, "search": true, "look": true, "up": true,
}

var nonAlpha = regexp.MustCompile(`[^a-z0-9' ]+`)

func splitKeywords(query string) []string {
	cleaned := nonAlpha.ReplaceAllString(strings.ToLower(query), " ")
	words := strings.Fields(cleaned)
	var keywords []string
	for _, w := range words {
		if !stopWords[w] && len(w) > 1 {
			keywords = append(keywords, w)
		}
	}
	return keywords
}
