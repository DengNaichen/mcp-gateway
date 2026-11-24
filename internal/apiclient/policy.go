package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"gatewaylab/internal/apitypes"
)

func (c *Client) Policies(ctx context.Context) (apitypes.PolicyOverview, error) {
	var out apitypes.PolicyOverview
	err := c.doJSON(ctx, http.MethodGet, "/policies", nil, &out)
	return out, err
}

func (c *Client) SavePolicyRule(ctx context.Context, rule apitypes.PolicyRule) (apitypes.PolicyRule, error) {
	var out apitypes.PolicyRule
	err := c.doJSON(ctx, http.MethodPut, "/policies/rules", rule, &out)
	return out, err
}

func (c *Client) DeletePolicyRule(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/policies/rules/"+url.PathEscape(id), nil, nil)
}

func (c *Client) EvaluatePolicy(ctx context.Context, req apitypes.PolicyEvaluateRequest) (apitypes.PolicyEvaluateResult, error) {
	var out apitypes.PolicyEvaluateResult
	err := c.doJSON(ctx, http.MethodPost, "/policies/evaluate", req, &out)
	return out, err
}

func (c *Client) QueryEvents(ctx context.Context, filter apitypes.EventFilter) (apitypes.EventPage, error) {
	query := url.Values{}
	for _, field := range []struct{ key, value string }{
		{"actor", filter.Actor}, {"repo_id", filter.RepoID}, {"service_id", filter.ServiceID},
		{"protocol", filter.Protocol}, {"decision", filter.Decision}, {"status", filter.Status},
		{"q", filter.Query}, {"before", filter.Before}, {"action", filter.Action}, {"skill_id", filter.SkillID},
	} {
		if field.value != "" {
			query.Set(field.key, field.value)
		}
	}
	if filter.Limit != 0 {
		query.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := "/events/query"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var out apitypes.EventPage
	err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
