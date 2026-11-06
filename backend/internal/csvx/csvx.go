// Package csvx holds the CSV cell rules shared by every export and import.
package csvx

import "strings"

// BOM makes spreadsheets read an exported file as UTF-8.
const BOM = "\xEF\xBB\xBF"

const formulaStarts = "=+-@\t\r"

// Safe stops a spreadsheet from running a cell as a formula.
func Safe(s string) string {
	if s != "" && strings.ContainsRune(formulaStarts, rune(s[0])) {
		return "'" + s
	}
	return s
}

// Unsafe reverses Safe: it strips one leading apostrophe when the character
// after it is one Safe guards, and leaves every other cell alone.
func Unsafe(s string) string {
	if len(s) > 1 && s[0] == '\'' && strings.ContainsRune(formulaStarts, rune(s[1])) {
		return s[1:]
	}
	return s
}
