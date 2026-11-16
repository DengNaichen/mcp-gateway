package jsonutil

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

func Digest(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

// MustDigest is used for internal snapshots composed solely of JSON values.
func MustDigest(v any) string {
	sum, err := Digest(v)
	if err != nil {
		panic("non-JSON maintenance value: " + err.Error())
	}
	return sum
}
