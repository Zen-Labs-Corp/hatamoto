package adminpath

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
)

// crockford is the Crockford base32 alphabet in lower case: no i, l, o or u,
// so nothing is ambiguous when read aloud or typed on a phone.
const crockford = "0123456789abcdefghjkmnpqrstvwxyz"

const generateTries = 10

// Generate returns a random address of the form /xxxx-xxxx-xxxx: 12 Crockford
// base32 characters (60 bits) from crypto/rand. It draws again, up to 10
// times, when Assess does not rank a draw strong (a rare run of neighbouring
// characters), and fails if none does.
func Generate() (string, error) {
	for range generateTries {
		p, err := draw()
		if err != nil {
			return "", err
		}
		if Assess(context.Background(), p, nil).Level == LevelStrong {
			return p, nil
		}
	}
	return "", errors.New("adminpath: no strong address after 10 draws")
}

func draw() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	v := binary.BigEndian.Uint64(b[:]) >> 4 // 60 bits
	var g [12]byte
	for i := 11; i >= 0; i-- {
		g[i] = crockford[v&31]
		v >>= 5
	}
	return "/" + string(g[0:4]) + "-" + string(g[4:8]) + "-" + string(g[8:12]), nil
}
