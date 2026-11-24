package api

import (
	"net/http"
	"strconv"

	"gatewaylab/internal/access"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/gateway"
)

func wireApproval(a gateway.Approval) apitypes.Approval { return apitypes.Approval(a) }

func (g *server) approvalAdmin(r *http.Request) bool {
	if g.access != nil {
		p, ok := access.PrincipalFrom(r.Context())
		return ok && p.Admin
	}
	return g.localAdmin != nil && g.localAdmin.Authenticate(r.Header.Get("X-Gateway-Admin-Token"))
}

func (g *server) registerApprovals(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/approvals", func(w http.ResponseWriter, r *http.Request) {
		if !g.approvalAdmin(r) {
			fail(w, problem(403, "查看审批单需要管理身份", nil))
			return
		}
		q := r.URL.Query()
		limit := 100
		if q.Has("limit") {
			var err error
			limit, err = strconv.Atoi(q.Get("limit"))
			if err != nil || limit < 1 {
				fail(w, invalid("limit 必须为正整数"))
				return
			}
		}
		out, _, err := g.gw.Approvals(r.Context(), q.Get("status"), limit)
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		respond(w, 200, mapSlice(out, wireApproval))
	})
	mux.HandleFunc("POST /api/approvals/{id}/approve", func(w http.ResponseWriter, r *http.Request) { g.decideApproval(w, r, "approved") })
	mux.HandleFunc("POST /api/approvals/{id}/deny", func(w http.ResponseWriter, r *http.Request) { g.decideApproval(w, r, "denied") })
}

func (g *server) decideApproval(w http.ResponseWriter, r *http.Request, decision string) {
	if !g.approvalAdmin(r) {
		fail(w, problem(403, "审批需要管理身份", nil))
		return
	}
	var in apitypes.ApprovalDecisionRequest
	if err := decode(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	out, err := g.gw.DecideApproval(r.Context(), r.PathValue("id"), decision, in.Note)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, wireApproval(out))
}
