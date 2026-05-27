// Package property is the writer for real-estate property activity-log
// pages under the obsidian vault's Properties/ directory. Each property
// is one markdown file; Log appends a `YYYY-MM-DD, <hours>, <description>`
// line under a stable "## Log" section. Sync via the shared obsidian
// helpers (Cmd, CommitAndPush).
package property

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/markusmobius/go-dateparser"

	"github.com/jcgregorio/myjarvis/internal/obsidian"
)

var dir = "/home/jcgregorio/obsidian/Properties"

// SetDir points the package at a non-default Properties/ path.
func SetDir(path string) { dir = path }

// Fetch returns the display names (filename without .md) of every
// property page. These are fed to the log_property_event tool as an enum
// so the LLM can only pick a property that actually exists.
func Fetch() ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read properties dir: %w", err)
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

// resolveProperty maps the LLM-supplied name to an existing page. The
// enum should already constrain it, so prefer an exact (case-insensitive)
// match; fall back only to an *unambiguous* substring match (exactly one
// page contains the phrase). Anything else is "not found" — the caller
// fails gracefully rather than risk writing to the wrong property.
func resolveProperty(name string) (string, bool) {
	names, err := Fetch()
	if err != nil {
		return "", false
	}
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return "", false
	}
	for _, n := range names {
		if strings.ToLower(n) == want {
			return n, true
		}
	}
	match, count := "", 0
	for _, n := range names {
		if strings.Contains(strings.ToLower(n), want) {
			match, count = n, count+1
		}
	}
	if count == 1 {
		return match, true
	}
	return "", false
}

// resolveDate turns a "when" phrase into a time.Time. Anything
// unrecognized falls back to `now` (the spoken confirmation echoes the
// date so the user can catch it).
func resolveDate(now time.Time, when string) time.Time {
	dt, err := dateparser.Parse(
		&dateparser.Configuration{CurrentTime: now}, when)
	if err != nil {
		return now
	}
	return dt.Time
}

func dateToMarkdown(t time.Time) string { return t.Format("2006-01-02") }

func dateToTTS(t time.Time) string {
	return fmt.Sprintf("%s %d", t.Month(), t.Day())
}

// appendUnderLogSection inserts line at the end of the page's "## Log"
// section, creating that section at EOF if it doesn't exist. Keeping the
// entries inside a dedicated section (vs raw EOF) makes them stable to
// parse later and survives Obsidian reordering other content.
func appendUnderLogSection(path, line string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read property page: %w", err)
	}
	var lines []string
	if s := strings.TrimRight(string(data), "\n"); s != "" {
		lines = strings.Split(s, "\n")
	}

	hdr := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "## Log" {
			hdr = i
			break
		}
	}
	if hdr == -1 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "## Log", line)
	} else {
		end := len(lines)
		for i := hdr + 1; i < len(lines); i++ {
			if strings.HasPrefix(lines[i], "## ") {
				end = i
				break
			}
		}
		ins := end
		for ins > hdr+1 && strings.TrimSpace(lines[ins-1]) == "" {
			ins--
		}
		out := append([]string{}, lines[:ins]...)
		out = append(out, line)
		lines = append(out, lines[ins:]...)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		return fmt.Errorf("write property page: %w", err)
	}
	return nil
}

// shortProp trims the address tail for a friendlier spoken confirmation
// ("5 Myrtle Ct. Ocean Isle Beach 28469" -> "5 Myrtle Ct").
func shortProp(page string) string {
	if i := strings.IndexAny(page, ".,"); i > 0 {
		return strings.TrimSpace(page[:i])
	}
	return page
}

// Log appends `YYYY-MM-DD, <hours>, <description>` under the property
// page's "## Log" section and syncs it via git. Returns a spoken
// confirmation (or a graceful spoken message if the property is unknown).
// The description is the final free-text CSV field and may contain
// commas — a reader splits on the first two commas only.
func Log(property string, hours int, description, when string) (string, error) {
	desc := strings.TrimSpace(strings.ReplaceAll(description, "\n", " "))
	if desc == "" {
		return "", fmt.Errorf("empty description")
	}
	page, ok := resolveProperty(property)
	if !ok {
		return fmt.Sprintf("I don't have a property page for %s.", property), nil
	}
	if hours < 0 {
		hours = 0
	}
	date := resolveDate(time.Now(), when)
	line := fmt.Sprintf("%s, %d, %s", dateToMarkdown(date), hours, desc)

	fileName := page + ".md"
	filePath := filepath.Join(dir, fileName)

	if err := obsidian.Cmd("pull", "--rebase"); err != nil {
		log.Printf("[property] git pull failed (continuing): %v", err)
	}
	if err := appendUnderLogSection(filePath, line); err != nil {
		return "", err
	}
	log.Printf("[property] logged to %s: %s", fileName, line)

	if err := obsidian.CommitAndPush(filepath.Join("Properties", fileName),
		fmt.Sprintf("Property log: %s (%dh) %s", page, hours, desc)); err != nil {
		return "", err
	}

	unit := "hours"
	if hours == 1 {
		unit = "hour"
	}
	return fmt.Sprintf("Logged for %s on %s: %d %s, %s.",
		shortProp(page), dateToTTS(date), hours, unit, desc), nil
}
