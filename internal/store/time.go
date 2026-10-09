package store

import (
	"crypto/rand"
	"fmt"
	"math"
	"strings"
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

// NewID is a site or post id: an uppercase RFC 4122 v4 UUID, like Planet's.
func NewID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}
