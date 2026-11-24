package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gatewaylab/internal/apitypes"
)

func TestApprovalClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gateway-Admin-Token") != "local-token" {
			t.Error("missing local credential")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/approvals":
			if r.URL.Query().Get("status") != "pending" || r.URL.Query().Get("limit") != "10" {
				t.Errorf("wrong filter: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[{"id":"apv_fixture","status":"pending"}]`))
		case "POST /api/approvals/apv_fixture/approve", "POST /api/approvals/apv_fixture/deny":
			var in apitypes.ApprovalDecisionRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Note != "reviewed" {
				t.Errorf("decision: %+v %v", in, err)
			}
			_, _ = w.Write([]byte(`{"id":"apv_fixture","decided_by":"local_admin"}`))
		case "POST /api/invoke":
			var in apitypes.InvokeRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if in.ApprovalID == "" {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"status":"approval_required","approval":{"approval_id":"apv_fixture","expires_in":1800}}`))
			} else {
				if in.ApprovalID != "apv_fixture" {
					t.Errorf("wrong approval: %s", in.ApprovalID)
				}
				_, _ = w.Write([]byte(`{"data":"done"}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, WithAdminToken("local-token"))
	req := apitypes.InvokeRequest{ToolID: "svc.change"}
	r, err := c.Invoke(t.Context(), req)
	if err != nil || r.Approval == nil || r.Status != "approval_required" {
		t.Fatalf("202 result: %+v %v", r, err)
	}
	req.ApprovalID = r.Approval.ApprovalID
	rows, err := c.Approvals(t.Context(), "pending", 10)
	if err != nil || len(rows) != 1 || rows[0].ID != req.ApprovalID {
		t.Fatalf("list: %+v %v", rows, err)
	}
	note := apitypes.ApprovalDecisionRequest{Note: "reviewed"}
	for _, decide := range []func(context.Context, string, apitypes.ApprovalDecisionRequest) (apitypes.Approval, error){c.Approve, c.Deny} {
		if a, err := decide(t.Context(), req.ApprovalID, note); err != nil || a.DecidedBy != "local_admin" {
			t.Fatalf("decision: %+v %v", a, err)
		}
	}
	r, err = c.Invoke(t.Context(), req)
	if err != nil || r.Data != "done" || r.Approval != nil {
		t.Fatalf("200 result: %+v %v", r, err)
	}
}
