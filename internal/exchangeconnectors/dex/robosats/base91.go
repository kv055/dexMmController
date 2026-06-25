package robosats

import "strings"

// base91Alphabet is the standard basE91 alphabet (Joachim Henke), the
// encoding RoboSats uses for the hashed robot token in the Authorization
// header.
const base91Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!#$%&()*+,./:;<=>?@[]^_`{|}~\""

// base91Encode encodes data as a basE91 string.
func base91Encode(data []byte) string {
	var sb strings.Builder
	b, n := 0, 0
	for _, c := range data {
		b |= int(c) << n
		n += 8
		if n > 13 {
			v := b & 8191
			if v > 88 {
				b >>= 13
				n -= 13
			} else {
				v = b & 16383
				b >>= 14
				n -= 14
			}
			sb.WriteByte(base91Alphabet[v%91])
			sb.WriteByte(base91Alphabet[v/91])
		}
	}
	if n > 0 {
		sb.WriteByte(base91Alphabet[b%91])
		if n > 7 || b > 90 {
			sb.WriteByte(base91Alphabet[b/91])
		}
	}
	return sb.String()
}
