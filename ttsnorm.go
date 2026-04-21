package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// NormalizeForTTS rewrites dates, prices, and other numeric formats into
// words that a TTS engine will pronounce naturally.
func NormalizeForTTS(text string) string {
	text = dateRe.ReplaceAllStringFunc(text, expandDate)
	text = yearRe.ReplaceAllStringFunc(text, expandYear)
	text = dollarRe.ReplaceAllStringFunc(text, expandDollar)
	text = percentRe.ReplaceAllStringFunc(text, expandPercent)
	return text
}

// M/D/YYYY, MM/DD/YYYY, or M/D/YY (not embedded in longer numbers)
var dateRe = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})/(\d{2,4})\b`)

var months = []string{
	"", "January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December",
}

func expandDate(s string) string {
	m := dateRe.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	month, _ := strconv.Atoi(m[1])
	day, _ := strconv.Atoi(m[2])
	year, _ := strconv.Atoi(m[3])
	if month < 1 || month > 12 {
		return s
	}
	// Expand 2-digit year: 00-49 → 2000s, 50-99 → 1900s
	if year < 100 {
		if year < 50 {
			year += 2000
		} else {
			year += 1900
		}
	}
	return fmt.Sprintf("%s %s, %s", months[month], ordinal(day), spellYear(year))
}

func ordinal(n int) string {
	suffix := "th"
	switch n % 10 {
	case 1:
		if n%100 != 11 {
			suffix = "st"
		}
	case 2:
		if n%100 != 12 {
			suffix = "nd"
		}
	case 3:
		if n%100 != 13 {
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

// Standalone 4-digit years (1900–2099) in prose, not preceded by $ or /
var yearRe = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)

func expandYear(s string) string {
	m := yearRe.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	y, _ := strconv.Atoi(m[1])
	return spellYear(y)
}

func spellYear(y int) string {
	if y == 2000 {
		return "two thousand"
	}
	if y > 2000 && y <= 2009 {
		return fmt.Sprintf("two thousand %s", smallNumber(y-2000))
	}
	if y >= 2010 && y <= 2099 {
		return fmt.Sprintf("twenty %s", smallNumber(y-2000))
	}
	// 1900-1999: "nineteen seventy six"
	if y >= 1900 && y <= 1999 {
		return fmt.Sprintf("nineteen %s", smallNumber(y-1900))
	}
	return strconv.Itoa(y)
}

var ones = []string{"", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine"}
var teens = []string{"ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen"}
var tens = []string{"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"}

func smallNumber(n int) string {
	if n == 0 {
		return ""
	}
	if n < 10 {
		return ones[n]
	}
	if n < 20 {
		return teens[n-10]
	}
	t := tens[n/10]
	o := ones[n%10]
	if o != "" {
		return t + " " + o
	}
	return t
}

// $1,234,567 or $1234567 or $1,234.56 — optional cents
var dollarRe = regexp.MustCompile(`\$[\d,]+(?:\.\d{1,2})?`)

func expandDollar(s string) string {
	// Strip $ and commas
	clean := strings.ReplaceAll(s[1:], ",", "")

	parts := strings.SplitN(clean, ".", 2)
	whole, _ := strconv.ParseInt(parts[0], 10, 64)
	cents := int64(0)
	if len(parts) == 2 {
		cents, _ = strconv.ParseInt(parts[1], 10, 64)
		// Handle single digit cents: $1.5 → 50 cents
		if len(parts[1]) == 1 {
			cents *= 10
		}
	}

	var result string
	switch {
	case whole >= 1_000_000:
		millions := whole / 1_000_000
		remainder := whole % 1_000_000
		if remainder == 0 {
			result = fmt.Sprintf("%d million dollars", millions)
		} else if remainder%1000 == 0 {
			result = fmt.Sprintf("%d million %d thousand dollars", millions, remainder/1000)
		} else {
			result = fmt.Sprintf("%d million %d dollars", millions, remainder)
		}
	case whole >= 1000:
		thousands := whole / 1000
		remainder := whole % 1000
		if remainder == 0 {
			result = fmt.Sprintf("%d thousand dollars", thousands)
		} else {
			result = fmt.Sprintf("%d thousand %d dollars", thousands, remainder)
		}
	default:
		result = fmt.Sprintf("%d dollars", whole)
	}

	if cents > 0 {
		result += fmt.Sprintf(" and %d cents", cents)
	}

	return result
}

// 5.25% or 0%
var percentRe = regexp.MustCompile(`(\d+(?:\.\d+)?)%`)

func expandPercent(s string) string {
	m := percentRe.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	return m[1] + " percent"
}
