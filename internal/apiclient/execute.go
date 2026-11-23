package apiclient

import (
	"context"
	"net/http"

	"gatewaylab/internal/apitypes"
)

// CLIExec sends a scoped CLI invocation. signScope, when supplied, receives
// the exact JSON bytes that will be sent to the gateway.
func (c *Client) CLIExec(ctx context.Context, in apitypes.CLIExecRequest, signScope func([]byte) (string, error)) (apitypes.InvokeResult, error) {
	var out apitypes.InvokeResult
	// Never forward a request, or its scope header, to a redirect target.
	direct := *c
	hc := *c.httpClient
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	direct.httpClient = &hc
	err := direct.doJSONWithScope(ctx, http.MethodPost, "/cli/exec", in, &out, signScope)
	return out, err
}
