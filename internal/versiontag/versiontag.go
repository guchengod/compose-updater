package versiontag

import (
	"regexp"
	"strconv"
	"strings"
)

var numericTagPattern = regexp.MustCompile(`^([vV]?)([0-9]+(?:\.[0-9]+)*)([-_][0-9A-Za-z][0-9A-Za-z._-]*)?$`)

type Version struct {
	Raw    string
	Prefix string
	Parts  []uint64
	Suffix string
}

func Parse(tag string) (Version, bool) {
	matches := numericTagPattern.FindStringSubmatch(strings.TrimSpace(tag))
	if matches == nil {
		return Version{}, false
	}
	partsText := strings.Split(matches[2], ".")
	parts := make([]uint64, 0, len(partsText))
	for _, part := range partsText {
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return Version{}, false
		}
		parts = append(parts, value)
	}
	return Version{Raw: tag, Prefix: matches[1], Parts: parts, Suffix: matches[3]}, true
}

// Latest returns the numerically highest tag in the same family as current.
// Family means the same optional v-prefix and the same variant suffix, so
// 1.2.3-alpine is only compared with other *-alpine tags.
func Latest(currentTag string, candidates []string) (string, bool) {
	current, ok := Parse(currentTag)
	if !ok {
		return "", false
	}
	best := current
	for _, candidateText := range candidates {
		candidate, ok := Parse(candidateText)
		if !ok || candidate.Prefix != current.Prefix || candidate.Suffix != current.Suffix {
			continue
		}
		if Compare(candidate, best) > 0 {
			best = candidate
		}
	}
	if Compare(best, current) <= 0 {
		return "", false
	}
	return best.Raw, true
}

func Compare(left, right Version) int {
	length := len(left.Parts)
	if len(right.Parts) > length {
		length = len(right.Parts)
	}
	for index := 0; index < length; index++ {
		var l, r uint64
		if index < len(left.Parts) {
			l = left.Parts[index]
		}
		if index < len(right.Parts) {
			r = right.Parts[index]
		}
		switch {
		case l > r:
			return 1
		case l < r:
			return -1
		}
	}
	return 0
}
