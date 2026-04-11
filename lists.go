package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const listsDir = "/home/jcgregorio/obsidian/Lists"
const obsidianRepo = "/home/jcgregorio/obsidian"

// FetchLists returns the display names of all .md files in the Lists directory.
// Names are derived from filenames by removing the .md extension.
func FetchLists() ([]string, error) {
	entries, err := os.ReadDir(listsDir)
	if err != nil {
		return nil, fmt.Errorf("read lists dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".md"))
	}
	return names, nil
}

// AddToList appends an item to a list file using Obsidian checkbox format.
// It performs git pull before writing and git add/commit/push after.
func AddToList(listName, item string) error {
	if listName == "" {
		listName = "ShoppingList"
	}

	// Normalize: the LLM might say "Shopping List" but the file is "ShoppingList.md"
	fileName := strings.ReplaceAll(listName, " ", "") + ".md"
	filePath := filepath.Join(listsDir, fileName)

	// Git pull first
	if err := gitCmd("pull", "--rebase"); err != nil {
		log.Printf("[lists] git pull failed (continuing): %v", err)
	}

	// Append the item
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open list file: %w", err)
	}
	if _, err := fmt.Fprintf(f, "- [ ] %s\n", item); err != nil {
		f.Close()
		return fmt.Errorf("write item: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close list file: %w", err)
	}

	log.Printf("[lists] added %q to %s", item, fileName)

	// Git add, commit, push
	relPath := filepath.Join("Lists", fileName)
	if err := gitCmd("add", relPath); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	commitMsg := fmt.Sprintf("Add %q to %s", item, listName)
	if err := gitCmd("commit", "-m", commitMsg); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	if err := gitCmd("push"); err != nil {
		return fmt.Errorf("git push: %w", err)
	}

	return nil
}

func gitCmd(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = obsidianRepo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}
