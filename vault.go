package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
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

// grepVault searches all .md files in the vault for any of the space-separated
// keywords in query. Returns matching file contents keyed by relative path.
func (v *VaultSearcher) grepVault(query string) (map[string]string, error) {
	keywords := splitKeywords(query)
	if len(keywords) == 0 {
		return nil, fmt.Errorf("no search keywords provided")
	}

	matches := make(map[string]string)

	err := filepath.Walk(v.vaultDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}

		lower := strings.ToLower(string(content))
		lowerName := strings.ToLower(info.Name())

		for _, kw := range keywords {
			if strings.Contains(lower, kw) || strings.Contains(lowerName, kw) {
				rel, _ := filepath.Rel(v.vaultDir, path)
				matches[rel] = string(content)
				break
			}
		}
		return nil
	})

	return matches, err
}

// askWithContext sends the matched documents plus the question to the LLM
// and returns a natural-language answer suitable for TTS.
func (v *VaultSearcher) askWithContext(ctx context.Context, question string, docs map[string]string) (string, error) {
	var b strings.Builder
	for path, content := range docs {
		fmt.Fprintf(&b, "--- File: %s ---\n%s\n\n", path, content)
	}

	log.Printf("[vault] sending %d doc(s) (%d bytes) to LLM", len(docs), b.Len())

	return v.llm.ChatPlain(ctx,
		"You are a helpful home assistant. Answer questions using the provided documents. "+
			"Give short, natural answers suitable for text-to-speech — no markdown, no lists, no special formatting. "+
			"Just speak naturally as if answering a person out loud.",
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
