package upstream

import (
	"context"
	"fmt"
	"gatewaylab/internal/fault"
	"net/http"
	"os"
	"time"
)

func CheckHTTPConnectivity(ctx context.Context, s Server, lookup func(string) string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, s.Endpoint, nil)
	if err != nil {
		return 0, fmt.Errorf("create api connectivity request: %w", err)
	}
	for k, v := range s.Headers {
		req.Header.Set(k, os.Expand(v, lookup))
	}
	response, err := (&http.Client{CheckRedirect: noRedirect}).Do(req)
	if err != nil {
		return 0, fault.New(fault.Upstream, "无法连接 HTTP API", err)
	}
	defer closeBody(response)
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return 0, fault.New(fault.Upstream, fmt.Sprintf("服务返回 HTTP %d，请检查认证请求头", response.StatusCode), nil)
	}
	if response.StatusCode >= 500 {
		return 0, fault.New(fault.Upstream, fmt.Sprintf("服务返回 HTTP %d", response.StatusCode), nil)
	}
	return response.StatusCode, nil
}
