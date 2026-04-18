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

	return gitCommitAndPush(filepath.Join("Lists", fileName), fmt.Sprintf("Add %q to %s", item, listName))
}

// ReadList returns the unchecked items from a list file, one per line.
// Checked-off items (- [x]) are filtered out.
func ReadList(listName string) (string, error) {
	if listName == "" {
		listName = "ShoppingList"
	}
	fileName := strings.ReplaceAll(listName, " ", "") + ".md"
	filePath := filepath.Join(listsDir, fileName)

	if err := gitCmd("pull", "--rebase"); err != nil {
		log.Printf("[lists] git pull failed (continuing): %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read list file: %w", err)
	}

	seen := make(map[string]bool)
	var active []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ] ") {
			item := strings.TrimPrefix(trimmed, "- [ ] ")
			key := strings.ToLower(item)
			if !seen[key] {
				seen[key] = true
				active = append(active, item)
			}
		}
	}
	if len(active) == 0 {
		return fmt.Sprintf("The %s list is empty.", listName), nil
	}
	return strings.Join(active, ", "), nil
}

// CheckOffItem marks all unchecked items matching the given string
// (case-insensitive substring match) as done by changing - [ ] to - [x].
func CheckOffItem(listName, item string) error {
	if listName == "" {
		listName = "ShoppingList"
	}
	fileName := strings.ReplaceAll(listName, " ", "") + ".md"
	filePath := filepath.Join(listsDir, fileName)

	if err := gitCmd("pull", "--rebase"); err != nil {
		log.Printf("[lists] git pull failed (continuing): %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read list file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	count := 0
	itemLower := strings.ToLower(item)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ] ") && strings.Contains(strings.ToLower(trimmed), itemLower) {
			lines[i] = strings.Replace(line, "- [ ] ", "- [x] ", 1)
			count++
		}
	}
	if count == 0 {
		return fmt.Errorf("%q not found on the %s list", item, listName)
	}

	log.Printf("[lists] checked off %d instance(s) of %q in %s", count, item, fileName)

	if err := os.WriteFile(filePath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		return fmt.Errorf("write list file: %w", err)
	}

	return gitCommitAndPush(filepath.Join("Lists", fileName), fmt.Sprintf("Check off %q in %s", item, listName))
}

// UncheckItem marks all checked items matching the given string
// (case-insensitive substring match) as unchecked by changing - [x] to - [ ].
func UncheckItem(listName, item string) error {
	if listName == "" {
		listName = "ShoppingList"
	}
	fileName := strings.ReplaceAll(listName, " ", "") + ".md"
	filePath := filepath.Join(listsDir, fileName)

	if err := gitCmd("pull", "--rebase"); err != nil {
		log.Printf("[lists] git pull failed (continuing): %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read list file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	count := 0
	itemLower := strings.ToLower(item)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [x] ") && strings.Contains(strings.ToLower(trimmed), itemLower) {
			lines[i] = strings.Replace(line, "- [x] ", "- [ ] ", 1)
			count++
		}
	}
	if count == 0 {
		return fmt.Errorf("%q not found (checked) on the %s list", item, listName)
	}

	log.Printf("[lists] unchecked %d instance(s) of %q in %s", count, item, fileName)

	if err := os.WriteFile(filePath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		return fmt.Errorf("write list file: %w", err)
	}

	return gitCommitAndPush(filepath.Join("Lists", fileName), fmt.Sprintf("Uncheck %q in %s", item, listName))
}

// CleanLists removes all checked-off items (- [x]) from every list file.
func CleanLists() error {
	if err := gitCmd("pull", "--rebase"); err != nil {
		log.Printf("[lists] git pull failed (continuing): %v", err)
	}

	entries, err := os.ReadDir(listsDir)
	if err != nil {
		return fmt.Errorf("read lists dir: %w", err)
	}

	var changed []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		filePath := filepath.Join(listsDir, e.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var kept []string
		removed := 0
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "- [x] ") {
				removed++
				continue
			}
			// Skip blank lines between items
			if trimmed == "" && len(kept) > 0 && strings.HasPrefix(strings.TrimSpace(kept[len(kept)-1]), "- [") {
				continue
			}
			kept = append(kept, line)
		}
		if removed == 0 {
			continue
		}

		if err := os.WriteFile(filePath, []byte(strings.Join(kept, "\n")), 0644); err != nil {
			log.Printf("[lists] failed to write %s: %v", e.Name(), err)
			continue
		}
		changed = append(changed, filepath.Join("Lists", e.Name()))
		log.Printf("[lists] cleaned %d items from %s", removed, e.Name())
	}

	if len(changed) == 0 {
		return nil
	}

	for _, rel := range changed {
		if err := gitCmd("add", rel); err != nil {
			return fmt.Errorf("git add %s: %w", rel, err)
		}
	}
	return gitCommitAndPush("", "Clean checked-off items from lists")
}

// gitCommitAndPush adds a file (if relPath is non-empty), commits, and pushes.
func gitCommitAndPush(relPath, commitMsg string) error {
	if relPath != "" {
		if err := gitCmd("add", relPath); err != nil {
			return fmt.Errorf("git add: %w", err)
		}
	}
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
