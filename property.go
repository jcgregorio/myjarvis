package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/markusmobius/go-dateparser"
)

// propertiesDir mirrors listsDir (lists.go). One .md page per property,
// under the same obsidian git repo, so it reuses gitCmd /
// gitCommitAndPush and is picked up by the same sync + Qdrant reindex.
var propertiesDir = "/home/jcgregorio/obsidian/Properties"

// FetchProperties returns the display names (filename without .md) of
// every property page, e.g. "5 Myrtle Ct. Ocean Isle Beach 28469".
// These are fed to the log_property_event tool as an enum so the LLM
// can only pick a property that actually exists.
func FetchProperties() ([]string, error) {
	entries, err := os.ReadDir(propertiesDir)
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
	names, err := FetchProperties()
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

// resolveDate turns a "when" phrase into YYYY-MM-DD. Anything unrecognized
// falls back to today (the spoken confirmation echoes the date so the user can
// catch it).
func resolveDate(now time.Time, when string) time.Time {
	dt, err := dateparser.Parse(
		&dateparser.Configuration{
			CurrentTime: now,
		}, when)
	var t time.Time
	if err != nil {
		t = now
	} else {
		t = dt.Time
	}
	return t
}

func dateToMarkdown(t time.Time) string {
	return t.Format("2006-01-02")
}

func dateToTTS(t time.Time) string {
	month := t.Month().String() // "May"
	dayOrdinal := t.Day()       // "20"
	return fmt.Sprintf("%s %d", month, dayOrdinal)
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

// LogPropertyEvent appends `YYYY-MM-DD, <hours>, <description>` under the
// property page's ## Log section and syncs it via git. Returns a spoken
// confirmation (or a graceful spoken message if the property is unknown).
// The description is the final free-text CSV field and may contain
// commas — a reader splits on the first two commas only.
func LogPropertyEvent(property string, hours int, description, when string) (string, error) {
	desc := strings.TrimSpace(strings.ReplaceAll(description, "\n", " "))
	if desc == "" {
		return "", fmt.Errorf("empty description")
	}
	page, ok := resolveProperty(property)
	if !ok {
		// Decision: fail gracefully, don't auto-create a page.
		return fmt.Sprintf("I don't have a property page for %s.", property), nil
	}
	if hours < 0 {
		hours = 0
	}
	date := resolveDate(time.Now(), when)
	line := fmt.Sprintf("%s, %d, %s", dateToMarkdown(date), hours, desc)

	fileName := page + ".md"
	filePath := filepath.Join(propertiesDir, fileName)

	if err := gitCmd("pull", "--rebase"); err != nil {
		log.Printf("[property] git pull failed (continuing): %v", err)
	}
	if err := appendUnderLogSection(filePath, line); err != nil {
		return "", err
	}
	log.Printf("[property] logged to %s: %s", fileName, line)

	if err := gitCommitAndPush(filepath.Join("Properties", fileName),
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
