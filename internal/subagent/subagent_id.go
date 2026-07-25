package subagent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// NewID generates a short, unique subagent ID suitable for use in a
// transcript filename and as a key in logs. Format: <unix-ms>-<4-byte-
// hex>, e.g. "1721912345678-a1b2c3d4". Unique enough for one session
// (timestamp + 8 hex chars from crypto/rand gives 32 bits of entropy
// per call, which is plenty to avoid collisions on the handful of
// subagents a single session will ever spawn).
func NewID() string {
	ts := time.Now().UnixMilli()
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read only fails on platforms where the system
		// CSPRNG is unavailable — extremely rare on any modern OS.
		// Fall back to all-zero random bytes so the runner still
		// produces a usable (if slightly less unique) ID.
		b = [4]byte{}
	}
	return fmt.Sprintf("%d-%s", ts, hex.EncodeToString(b[:]))
}
