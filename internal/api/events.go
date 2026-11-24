package api

import (
	"errors"
	"net/http"
	"strconv"

	"gatewaylab/internal/access"
	"gatewaylab/internal/activity"
	"gatewaylab/internal/apitypes"
)

func (g *server) registerEvents(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, mapSlice(g.activity.Recent(), wireEvent))
	})
	mux.HandleFunc("GET /api/events/query", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filter := activity.Filter{
			Actor: q.Get("actor"), RepoID: q.Get("repo_id"), ServiceID: q.Get("service_id"),
			Protocol: q.Get("protocol"), Decision: q.Get("decision"), Status: q.Get("status"),
			Query: q.Get("q"), Before: q.Get("before"), Action: q.Get("action"), SkillID: q.Get("skill_id"),
		}
		if q.Has("limit") {
			limit, err := strconv.Atoi(q.Get("limit"))
			if err != nil || limit < 1 {
				fail(w, invalid("limit 必须为正整数"))
				return
			}
			filter.Limit = limit
		}
		if p, ok := access.PrincipalFrom(r.Context()); ok && !p.Admin {
			filter.Actor = p.ID
		}
		page, err := g.activity.Query(filter)
		if errors.Is(err, activity.ErrInvalidQuery) {
			fail(w, invalid("before 必须为 RFC3339Nano 时间戳或 time|id 游标"))
			return
		}
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		respond(w, http.StatusOK, apitypes.EventPage{
			Items: mapSlice(page.Items, wireEvent), NextCursor: page.NextCursor, HasMore: page.HasMore,
		})
	})
}
