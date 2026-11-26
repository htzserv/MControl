package version

import (
	"strconv"
	"strings"
)

const Current = "1.6.9"

// Compare compares two 4-segment version strings (e.g. "1.4.0.0" vs "1.4.0.1").
// Returns 1 if a > b, -1 if a < b, 0 if equal.
func Compare(a, b string) int {
	pa := parse(a)
	pb := parse(b)
	for i := 0; i < 4; i++ {
		if pa[i] > pb[i] {
			return 1
		}
		if pa[i] < pb[i] {
			return -1
		}
	}
	return 0
}

func parse(v string) [4]int {
	v = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v"))
	parts := strings.Split(v, ".")
	var out [4]int
	for i := 0; i < 4 && i < len(parts); i++ {
		numStr := parts[i]
		if idx := strings.IndexAny(numStr, "-+"); idx >= 0 {
			numStr = numStr[:idx]
		}
		n, _ := strconv.Atoi(numStr)
		out[i] = n
	}
	return out
}
