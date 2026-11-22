package seo

import (
	"fmt"
	"unicode/utf8"
)

const (
	// DefaultAIPosts is how many posts llms.txt lists when none is saved.
	DefaultAIPosts = 20
	// DefaultFullCapKB is the llms-full.txt size cap in KiB when none is saved.
	DefaultFullCapKB = 2048

	MaxAIPosts        = 200
	minFullCapKB      = 64
	maxFullCapKB      = 20480
	MaxAISummaryRunes = 300
)

// AIFiles drives /llms.txt and /llms-full.txt. The zero value is the default:
// both files on. Switches are stored inverted so documents saved before this
// key existed keep both files published.
type AIFiles struct {
	LLMsOff   bool   `json:"llms_off,omitzero"`
	FullOff   bool   `json:"full_off,omitzero"`
	Summary   string `json:"summary,omitzero"`
	Posts     int    `json:"posts,omitzero"`       // 0 = DefaultAIPosts
	FullCapKB int    `json:"full_cap_kb,omitzero"` // 0 = DefaultFullCapKB
}

// IsZero reports whether no AI files setting is saved.
func (a AIFiles) IsZero() bool {
	return a == AIFiles{}
}

// EffectivePosts is the number of posts llms.txt lists.
func (a AIFiles) EffectivePosts() int {
	if a.Posts <= 0 || a.Posts > MaxAIPosts {
		return DefaultAIPosts
	}
	return a.Posts
}

// EffectiveFullCapBytes is the llms-full.txt size cap in bytes.
func (a AIFiles) EffectiveFullCapBytes() int {
	if a.FullCapKB < minFullCapKB || a.FullCapKB > maxFullCapKB {
		return DefaultFullCapKB << 10
	}
	return a.FullCapKB << 10
}

// validateAIFiles checks the post count, the size cap and the summary length.
func validateAIFiles(a AIFiles) []FieldError {
	var errs []FieldError
	if a.Posts != 0 && (a.Posts < 1 || a.Posts > MaxAIPosts) {
		errs = append(errs, FieldError{"ai_files[posts]", fmt.Sprintf("Enter a number from 1 to %d.", MaxAIPosts)})
	}
	if a.FullCapKB != 0 && (a.FullCapKB < minFullCapKB || a.FullCapKB > maxFullCapKB) {
		errs = append(errs, FieldError{"ai_files[full_cap_kb]", fmt.Sprintf("Enter a size from %d to %d KB.", minFullCapKB, maxFullCapKB)})
	}
	if utf8.RuneCountInString(a.Summary) > MaxAISummaryRunes {
		errs = append(errs, FieldError{"ai_files[summary]", fmt.Sprintf("The summary can be at most %d characters.", MaxAISummaryRunes)})
	}
	return errs
}
