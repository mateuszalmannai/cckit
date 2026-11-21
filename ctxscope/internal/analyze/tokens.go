package analyze

import (
	"math"
	"unicode/utf8"
)

// EstimateTokens approximates Claude's tokenizer: about 3.8 characters per token for ASCII
// text and roughly one token per non-ASCII character (CJK, symbols). It is only a weight for
// splitting the API-reported total between chunks, so per-chunk numbers are approximate while
// the sum is exact once calibrated.
func EstimateTokens(s string) int {
	ascii, other := 0, 0
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf {
			ascii++
			i++
			continue
		}
		_, n := utf8.DecodeRuneInString(s[i:])
		other++
		i += n
	}
	return int(math.Ceil(float64(ascii)/3.8)) + other
}

// calibrate scales every chunk's estimate so the chunks sum to actual. With actual <= 0 the
// estimates are used as they are.
func calibrate(r *Result, actual int) {
	sum := 0
	for _, c := range r.Chunks {
		sum += c.Estimate
	}
	if actual <= 0 || sum == 0 {
		for i := range r.Chunks {
			r.Chunks[i].Tokens = r.Chunks[i].Estimate
		}
		r.Total = sum
		return
	}
	// Largest-remainder rounding keeps the sum exactly equal to actual.
	scale := float64(actual) / float64(sum)
	type rem struct {
		i int
		f float64
	}
	rems := make([]rem, len(r.Chunks))
	assigned := 0
	for i, c := range r.Chunks {
		exact := float64(c.Estimate) * scale
		r.Chunks[i].Tokens = int(exact)
		assigned += r.Chunks[i].Tokens
		rems[i] = rem{i, exact - math.Floor(exact)}
	}
	for left := actual - assigned; left > 0; left-- {
		best := 0
		for j := range rems {
			if rems[j].f > rems[best].f {
				best = j
			}
		}
		r.Chunks[rems[best].i].Tokens++
		rems[best].f = -1
	}
	r.Total = actual
	r.Calibrated = true
}
