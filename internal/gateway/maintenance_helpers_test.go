package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode job input: %w", err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		return fmt.Errorf("write job input: %w", err)
	}
	return nil
}
func serverTools(reg Registry, id string) []Tool {
	out := []Tool{}
	for _, t := range reg.Tools {
		if t.ServerID == id {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b Tool) int { return strings.Compare(a.ID, b.ID) })
	return out
}
