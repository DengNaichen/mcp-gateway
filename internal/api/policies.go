package api

import (
	"net/http"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/gateway"
	"gatewaylab/internal/policy"
)

func (g *server) registerPolicies(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/policies", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, wirePolicyOverview(g.gw.PolicyOverview()))
	})
	mux.HandleFunc("PUT /api/policies/rules", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.PolicyRule
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		rule, err := g.gw.SavePolicyRule(policy.Rule(in))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, wirePolicyRule(rule))
	})
	mux.HandleFunc("DELETE /api/policies/rules/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := g.gw.DeletePolicyRule(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, apitypes.Deleted{Deleted: true})
	})
	mux.HandleFunc("POST /api/policies/evaluate", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.PolicyEvaluateRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		result, err := g.gw.EvaluatePolicy(r.Context(), gateway.PolicyEvalRequest(in))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, wirePolicyEvaluation(result))
	})
}

func wirePolicyRule(r policy.Rule) apitypes.PolicyRule { return apitypes.PolicyRule(r) }

func wirePolicyOverview(o gateway.PolicyOverview) apitypes.PolicyOverview {
	return apitypes.PolicyOverview{Enforce: o.Enforce, BaselineFrom: o.BaselineFrom, Rules: mapSlice(o.Rules, wirePolicyRule)}
}

func wirePolicyEvaluation(r gateway.PolicyEvalResult) apitypes.PolicyEvaluateResult {
	d := r.Decision
	return apitypes.PolicyEvaluateResult{
		Effect: d.Effect, RuleID: d.RuleID, Reason: d.Reason, Trust: r.Trust,
		Trace: mapSlice(d.Trace, func(step policy.Step) apitypes.PolicyTraceStep { return apitypes.PolicyTraceStep(step) }),
	}
}
