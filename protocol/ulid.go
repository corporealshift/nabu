package protocol

import (
	"crypto/rand"
	"errors"
	"time"
)

// ULIDs (spec §2): 48 bits of millisecond time + 80 random bits, encoded as 26
// characters of Crockford base32. This is a minimal, dependency-free
// implementation; it does not guarantee monotonic ordering within one
// millisecond, which the daemon does not need because log order is positional.

const ulidAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var ulidDecode = func() [256]int8 {
	var t [256]int8
	for i := range t {
		t[i] = -1
	}
	for i := 0; i < len(ulidAlphabet); i++ {
		t[ulidAlphabet[i]] = int8(i)
	}
	// Crockford decoding tolerates lowercase; the spec's regex does not, so we
	// only accept uppercase here to stay consistent with Kotlin.
	return t
}()

// NewULID returns a fresh ULID for the current time.
func NewULID() string {
	return NewULIDAt(time.Now())
}

// NewULIDAt returns a ULID whose time component is t.
func NewULIDAt(t time.Time) string {
	var b [16]byte
	ms := uint64(t.UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	if _, err := rand.Read(b[6:]); err != nil {
		panic("protocol: crypto/rand failed: " + err.Error())
	}
	return encodeULID(b)
}

func encodeULID(b [16]byte) string {
	var out [26]byte
	// 128 bits → 26 base32 chars, most significant first. The top char holds
	// only 3 bits, hence the leading-character range 0..7 in the spec regex.
	out[0] = ulidAlphabet[(b[0]&224)>>5]
	out[1] = ulidAlphabet[b[0]&31]
	out[2] = ulidAlphabet[(b[1]&248)>>3]
	out[3] = ulidAlphabet[((b[1]&7)<<2)|((b[2]&192)>>6)]
	out[4] = ulidAlphabet[(b[2]&62)>>1]
	out[5] = ulidAlphabet[((b[2]&1)<<4)|((b[3]&240)>>4)]
	out[6] = ulidAlphabet[((b[3]&15)<<1)|((b[4]&128)>>7)]
	out[7] = ulidAlphabet[(b[4]&124)>>2]
	out[8] = ulidAlphabet[((b[4]&3)<<3)|((b[5]&224)>>5)]
	out[9] = ulidAlphabet[b[5]&31]
	out[10] = ulidAlphabet[(b[6]&248)>>3]
	out[11] = ulidAlphabet[((b[6]&7)<<2)|((b[7]&192)>>6)]
	out[12] = ulidAlphabet[(b[7]&62)>>1]
	out[13] = ulidAlphabet[((b[7]&1)<<4)|((b[8]&240)>>4)]
	out[14] = ulidAlphabet[((b[8]&15)<<1)|((b[9]&128)>>7)]
	out[15] = ulidAlphabet[(b[9]&124)>>2]
	out[16] = ulidAlphabet[((b[9]&3)<<3)|((b[10]&224)>>5)]
	out[17] = ulidAlphabet[b[10]&31]
	out[18] = ulidAlphabet[(b[11]&248)>>3]
	out[19] = ulidAlphabet[((b[11]&7)<<2)|((b[12]&192)>>6)]
	out[20] = ulidAlphabet[(b[12]&62)>>1]
	out[21] = ulidAlphabet[((b[12]&1)<<4)|((b[13]&240)>>4)]
	out[22] = ulidAlphabet[((b[13]&15)<<1)|((b[14]&128)>>7)]
	out[23] = ulidAlphabet[(b[14]&124)>>2]
	out[24] = ulidAlphabet[((b[14]&3)<<3)|((b[15]&224)>>5)]
	out[25] = ulidAlphabet[b[15]&31]
	return string(out[:])
}

// NewULIDAfter returns a fresh ULID guaranteed to sort strictly after prev.
// Plain ULIDs only order by millisecond: two generated in the same
// millisecond order by their random bits, which is not creation order. Any
// sequence that must stay in order (a session's events, a store's sessions)
// generates through this.
func NewULIDAfter(prev string) string {
	id := NewULID()
	if prev == "" || id > prev {
		return id
	}
	t, err := ULIDTime(prev)
	if err != nil {
		return id // prev is not a ULID; nothing to order against
	}
	// A later millisecond raises the leading 10 characters, so the result
	// sorts after prev whatever the random bits are.
	return NewULIDAt(t.Add(time.Millisecond))
}

// ErrInvalidULID is returned by ValidateULID.
var ErrInvalidULID = errors.New("invalid ULID")

// ValidateULID checks length, alphabet, and the leading-character bound.
func ValidateULID(s string) error {
	if len(s) != 26 {
		return ErrInvalidULID
	}
	if s[0] < '0' || s[0] > '7' {
		return ErrInvalidULID
	}
	for i := 0; i < len(s); i++ {
		if ulidDecode[s[i]] < 0 {
			return ErrInvalidULID
		}
	}
	return nil
}

// ULIDTime extracts the millisecond timestamp from a valid ULID.
func ULIDTime(s string) (time.Time, error) {
	if err := ValidateULID(s); err != nil {
		return time.Time{}, err
	}
	var ms uint64
	for i := 0; i < 10; i++ {
		ms = ms<<5 | uint64(ulidDecode[s[i]])
	}
	return time.UnixMilli(int64(ms)).UTC(), nil
}
