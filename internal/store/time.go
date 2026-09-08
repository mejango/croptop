package store

import (
	"math"
	"time"
)

// Planet writes every timestamp as seconds since 2001-01-01 UTC (Swift's
// Date reference). The template JavaScript adds this offset. We keep the format.
const appleEpochOffset = 978307200

type AppleTime float64

func FromTime(t time.Time) AppleTime {
	return AppleTime(float64(t.UnixNano())/1e9 - appleEpochOffset)
}

func Now() AppleTime { return FromTime(time.Now()) }

func (a AppleTime) Unix() int64 { return int64(math.Floor(float64(a) + appleEpochOffset)) }

func (a AppleTime) Time() time.Time {
	sec, frac := math.Modf(float64(a) + appleEpochOffset)
	return time.Unix(int64(sec), int64(frac*1e9)).UTC()
}
