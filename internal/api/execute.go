package api

import (
	"net/http"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/cliexec"
	"gatewaylab/internal/gateway"
)

func (g *server) registerExecute(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/discover", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.DiscoverRequest
		rawBody, err := decodeRaw(w, r, &in)
		if err != nil {
			fail(w, err)
			return
		}
		scopedCtx, err := g.resolveRESTScope(r.Context(), r, "discover_tools", rawBody, mapPtr(in.SimScope, domainSimScope))
		if err != nil {
			fail(w, err)
			return
		}
		out, err := g.gw.Discover(scopedCtx, domainDiscoverRequest(in))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, discoveryPresentation(out, in.SummaryOnly))
	})
	mux.HandleFunc("POST /api/invoke", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.InvokeRequest
		rawBody, err := decodeRaw(w, r, &in)
		if err != nil {
			fail(w, err)
			return
		}
		scopedCtx, err := g.resolveRESTScope(r.Context(), r, "invoke_tool", rawBody, mapPtr(in.SimScope, domainSimScope))
		if err != nil {
			fail(w, err)
			return
		}
		out, err := g.gw.Invoke(scopedCtx, domainInvokeRequest(in))
		if err != nil {
			fail(w, err)
			return
		}
		if out.Approval != nil {
			respond(w, 202, wireInvokeResult(out))
			return
		}
		respond(w, 200, wireInvokeResult(out))
	})
	mux.HandleFunc("POST /api/cli/exec", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.CLIExecRequest
		rawBody, err := decodeRaw(w, r, &in)
		if err != nil {
			fail(w, err)
			return
		}
		scopedCtx, err := g.resolveRESTScope(r.Context(), r, "cli_exec", rawBody, mapPtr(in.SimScope, domainSimScope))
		if err != nil {
			fail(w, err)
			return
		}
		toolID, args, err := cliexec.ParseGHArgs(in.ServiceID, in.Argv)
		if err != nil {
			fail(w, err)
			return
		}
		out, err := g.gw.Invoke(scopedCtx, gateway.InvokeRequest{
			ToolID: toolID, Arguments: args, Actor: in.Actor, SkillID: in.SkillID, ApprovalID: in.ApprovalID, CLIServiceID: in.ServiceID,
		})
		if err != nil {
			fail(w, err)
			return
		}
		if out.Approval != nil {
			respond(w, 202, wireInvokeResult(out))
			return
		}
		respond(w, 200, wireInvokeResult(out))
	})
}
