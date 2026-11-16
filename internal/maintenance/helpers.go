package maintenance

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

func newID() string                                           { return fmt.Sprintf("%x", time.Now().UnixNano()) }
func noRedirect(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
func closeBody(resp *http.Response) {
	if err := resp.Body.Close(); err != nil {
		slog.Debug("close response body", "error", err)
	}
}
