package retry

import (
	"math"
	"math/rand/v2"
	"time"
)

// cappedDelay is min(max, base*2^(k-1)). k is the 1-based retry number.
// A shift that does not fit in int64 returns max.
func cappedDelay(base, maxDelay time.Duration, k int) time.Duration {
	shift := k - 1
	if shift >= 63 || base > (time.Duration(math.MaxInt64)>>shift) {
		return maxDelay
	}
	d := base << shift
	if d > maxDelay {
		return maxDelay
	}
	return d
}

// jitter is a uniform duration in the closed interval [0, delay].
func jitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	return time.Duration(rand.Uint64N(uint64(delay) + 1))
}
