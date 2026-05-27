// Package lists is the writer for Obsidian-style checklist pages under
// the vault's Lists/ directory. Each list is a markdown file with
// "- [ ] item" / "- [x] item" lines. All mutating operations
// pull-before-write and commit+push afterwards via the shared
// obsidian.Cmd / obsidian.CommitAndPush helpers.
package lists

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/jcgregorio/myjarvis/internal/obsidian"
)

var dir = "/home/jcgregorio/obsidian/Lists"

// SetDir points the package at a non-default Lists/ path.
func SetDir(path string) { dir = path }

// Fetch returns the display names (filename without .md) of every list
// file in the configured directory.
func Fetch() ([]string, error) {
	entries, err := os.ReadDir(dir)
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

// Add appends an item to a list file using Obsidian checkbox format.
// Performs git pull before writing and git add/commit/push after.
func Add(listName, item string) error {
	if listName == "" {
		listName = "ShoppingList"
	}
	fileName := strings.ReplaceAll(listName, " ", "") + ".md"
	filePath := filepath.Join(dir, fileName)

	if err := obsidian.Cmd("pull", "--rebase"); err != nil {
		log.Printf("[lists] git pull failed (continuing): %v", err)
	}

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

	return obsidian.CommitAndPush(filepath.Join("Lists", fileName),
		fmt.Sprintf("Add %q to %s", item, listName))
}

// Read returns the unchecked items from a list file as a comma-joined
// string. Checked-off items (- [x]) are filtered out.
func Read(listName string) (string, error) {
	if listName == "" {
		listName = "ShoppingList"
	}
	fileName := strings.ReplaceAll(listName, " ", "") + ".md"
	filePath := filepath.Join(dir, fileName)

	if err := obsidian.Cmd("pull", "--rebase"); err != nil {
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

// CheckOff marks all unchecked items matching the given string
// (case-insensitive substring match) as done by changing - [ ] to - [x].
func CheckOff(listName, item string) error {
	if listName == "" {
		listName = "ShoppingList"
	}
	fileName := strings.ReplaceAll(listName, " ", "") + ".md"
	filePath := filepath.Join(dir, fileName)

	if err := obsidian.Cmd("pull", "--rebase"); err != nil {
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

	return obsidian.CommitAndPush(filepath.Join("Lists", fileName),
		fmt.Sprintf("Check off %q in %s", item, listName))
}

// Uncheck marks all checked items matching the given string
// (case-insensitive substring match) as unchecked by changing - [x] to - [ ].
func Uncheck(listName, item string) error {
	if listName == "" {
		listName = "ShoppingList"
	}
	fileName := strings.ReplaceAll(listName, " ", "") + ".md"
	filePath := filepath.Join(dir, fileName)

	if err := obsidian.Cmd("pull", "--rebase"); err != nil {
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

	return obsidian.CommitAndPush(filepath.Join("Lists", fileName),
		fmt.Sprintf("Uncheck %q in %s", item, listName))
}

// Clean removes all checked-off items (- [x]) from every list file.
func Clean() error {
	if err := obsidian.Cmd("pull", "--rebase"); err != nil {
		log.Printf("[lists] git pull failed (continuing): %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read lists dir: %w", err)
	}

	var changed []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		filePath := filepath.Join(dir, e.Name())
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
		if err := obsidian.Cmd("add", rel); err != nil {
			return fmt.Errorf("git add %s: %w", rel, err)
		}
	}
	return obsidian.CommitAndPush("", "Clean checked-off items from lists")
}
