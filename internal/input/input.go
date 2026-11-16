package input

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

func ReadBounded(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read bounded input: %w", err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("input exceeds %d bytes", limit)
	}
	return b, nil
}
func ReadFileBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open input file: %w", err)
	}
	b, readErr := ReadBounded(f, limit)
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close input file: %w", closeErr)
	}
	return b, nil
}
func EnsureJSONEnd(dec *json.Decoder, invalid func(string) error) error {
	var more any
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		return invalid("产物必须是单个 JSON 对象")
	}
	return nil
}
