package apiclient

import (
	"context"
	"net/http"
	"net/url"

	"gatewaylab/internal/apitypes"
)

func (c *Client) Skills(ctx context.Context) ([]apitypes.Skill, error) {
	var out []apitypes.Skill
	err := c.doJSON(ctx, http.MethodGet, "/skills", nil, &out)
	return out, err
}

func (c *Client) SaveSkill(ctx context.Context, skill apitypes.Skill) (apitypes.Skill, error) {
	var out apitypes.Skill
	err := c.doJSON(ctx, http.MethodPut, "/skills", skill, &out)
	return out, err
}

func (c *Client) DeleteSkill(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/skills/"+url.PathEscape(id), nil, nil)
}
