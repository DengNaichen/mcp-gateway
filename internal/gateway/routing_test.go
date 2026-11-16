package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gatewaylab/internal/model"
	"gatewaylab/internal/routing"
)

const systemOneMaxQuestions = model.SystemOneMaxQuestions
const openAIMaxTools = model.OpenAIMaxTools
const choiceQuestionName = model.ChoiceQuestionName

type decisionQuestion = model.Question

func toolQuestionName(i int) string { return fmt.Sprintf("tool_%d", i) }

func selectWithModel(ctx context.Context, s Settings, key string, req DiscoverRequest, tools []Tool) (routing.Decision, routing.Usage, error) {
	return selectTools(ctx, s, key, req, tools, routing.Options{})
}
func selectTools(ctx context.Context, s Settings, key string, req DiscoverRequest, tools []Tool, opts routing.Options) (routing.Decision, routing.Usage, error) {
	d, u, err := routing.SelectTools(ctx, routingModel{s, key}, s.MaxTools, routingRequest(req), tools, opts)
	return d, u, routingError(err)
}

// Both callbacks return model domain errors through routing.Client; Gateway is
// the boundary that restores the existing public HTTP status and safe message.
func TestRoutingModelErrorMapping(t *testing.T) {
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("provider echoed live-key"))
	}))
	defer host.Close()
	for _, method := range []string{"decisions", "choice"} {
		for _, invalidConfig := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/invalid=%v", method, invalidConfig), func(t *testing.T) {
				settings := model.Settings{BaseURL: host.URL, Model: "fixture", TimeoutSeconds: 5}
				wantKind, wantStatus := model.ErrUpstream, http.StatusBadGateway
				wantMessage := "Decisions 服务返回 HTTP 503: provider echoed [redacted]"
				if invalidConfig {
					settings.BaseURL += "/chat/completions"
					wantKind, wantStatus = model.ErrInvalid, http.StatusBadRequest
					wantMessage = "当前使用 Decisions API，请将 Base URL 改为 https://api.openai.com/v1 或完整 /decisions 地址"
				}
				client := routingModel{settings: settings, key: "live-key"}
				var err error
				if method == "decisions" {
					_, err = client.Decisions(context.Background(), "task", []string{"relevant?"})
				} else {
					_, _, err = client.Choose(context.Background(), "task", "pick", []routing.Choice{{Value: "s.one"}, {Value: "none"}})
				}
				if !errors.Is(err, wantKind) {
					t.Fatalf("expected model domain error %v, got %v", wantKind, err)
				}
				mapped := routingError(err)
				var public *appError
				if !errors.As(mapped, &public) || public.status != wantStatus || public.message != wantMessage || strings.Contains(mapped.Error(), "live-key") {
					t.Fatalf("status/message changed: %+v", mapped)
				}
			})
		}
	}
}
