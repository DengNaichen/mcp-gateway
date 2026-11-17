package upstream

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"gatewaylab/internal/input"
	"gatewaylab/internal/registry"
)

// FetchDocument downloads a definition document (such as an OpenAPI spec) of
// at most limit bytes. Redirects are followed because published specs are
// commonly served behind them.
func FetchDocument(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	if err := registry.ValidateURL(rawURL); err != nil {
		return nil, invalid("文档 URL 必须是有效的 HTTP(S) 地址")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create document request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, failure("下载文档失败", err)
	}
	defer closeBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, failure(fmt.Sprintf("下载文档失败: HTTP %d", resp.StatusCode), nil)
	}
	b, err := input.ReadBounded(resp.Body, limit)
	if err != nil {
		return nil, invalid(fmt.Sprintf("文档超过 %d MB 上限", limit>>20))
	}
	return b, nil
}
