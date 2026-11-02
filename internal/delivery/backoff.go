package delivery

import (
	"math"
	"time"
)

// retryDelays is the wait after attempt 1, then 2, and so on through attempt 7.
// There is no delay after attempt 8: that delivery has failed for good.
var retryDelays = [...]time.Duration{
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	6 * time.Hour,
	12 * time.Hour,
}

// NextDelay is the wait before a delivery may be claimed again.
// attemptsMade is the attempt that just finished, so 1 means the first try is done.
// ok is false when attemptsMade is below 1 or at least 8, because the schedule has ended.
//
// jitter returns a number from -1 to 1. -1 shortens the listed delay by 20%,
// 0 keeps it, and 1 lengthens it by 20%. Values outside that range are pulled
// back in, so the result always stays inside the 20% band.
func NextDelay(attemptsMade int, jitter func() float64) (time.Duration, bool) {
	if attemptsMade < 1 || attemptsMade >= 8 || jitter == nil {
		return 0, false
	}
	sample := jitter()
	if sample < -1 {
		sample = -1
	}
	if sample > 1 {
		sample = 1
	}
	percent := 100 + int64(math.Round(sample*20))
	base := retryDelays[attemptsMade-1]
	return time.Duration(int64(base) * percent / 100), true
}
