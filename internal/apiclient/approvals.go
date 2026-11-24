package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"gatewaylab/internal/apitypes"
)

func (c *Client) Approvals(ctx context.Context, status string, limit int) ([]apitypes.Approval, error) {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/approvals"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out []apitypes.Approval
	err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) Approve(ctx context.Context, id string, req apitypes.ApprovalDecisionRequest) (apitypes.Approval, error) {
	return c.decideApproval(ctx, id, "approve", req)
}

func (c *Client) Deny(ctx context.Context, id string, req apitypes.ApprovalDecisionRequest) (apitypes.Approval, error) {
	return c.decideApproval(ctx, id, "deny", req)
}

func (c *Client) decideApproval(ctx context.Context, id, action string, req apitypes.ApprovalDecisionRequest) (apitypes.Approval, error) {
	var out apitypes.Approval
	err := c.doJSON(ctx, http.MethodPost, "/approvals/"+url.PathEscape(id)+"/"+action, req, &out)
	return out, err
}
