package api

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gatewaylab/internal/access"
	"gatewaylab/internal/activity"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/gateway"
	"gatewaylab/internal/maintenance"
	"gatewaylab/internal/market"
	"gatewaylab/internal/model"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/routing"
	"gatewaylab/internal/scope"
)

// jsonShape renders the JSON encoding shape of t: object keys with omitempty,
// and element shapes, recursively. Named types are erased, so a domain type and
// its wire twin have the same shape exactly when they encode the same way.
func jsonShape(t reflect.Type, seen map[reflect.Type]bool) string {
	if t.Kind() == reflect.Pointer {
		return "*" + jsonShape(t.Elem(), seen)
	}
	switch t.String() {
	case "time.Time":
		return "time"
	case "json.RawMessage":
		return "raw"
	}
	switch t.Kind() {
	case reflect.Slice:
		return "[]" + jsonShape(t.Elem(), seen)
	case reflect.Map:
		return "map[" + t.Key().Kind().String() + "]" + jsonShape(t.Elem(), seen)
	case reflect.Struct:
		if seen[t] {
			return "<cycle>"
		}
		seen[t] = true
		defer delete(seen, t)
		fields := structFields(t, seen)
		keys := make([]string, 0, len(fields))
		for k, v := range fields {
			keys = append(keys, k+":"+v)
		}
		slices.Sort(keys)
		return "{" + strings.Join(keys, ",") + "}"
	default:
		return t.Kind().String()
	}
}

func structFields(t reflect.Type, seen map[reflect.Type]bool) map[string]string {
	out := map[string]string{}
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" || !f.IsExported() {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			for k, v := range structFields(f.Type, seen) {
				out[k] = v
			}
			continue
		}
		if name == "" {
			name = f.Name
		}
		if strings.Contains(opts, "omitempty") {
			name += "?"
		}
		out[name] = jsonShape(f.Type, seen)
	}
	return out
}

// TestWireTypesMirrorDomain fails when a domain type gains, loses or reshapes a
// JSON field without the matching apitypes change, so the contract cannot drift
// silently. wireOnly lists request fields that exist only on the wire.
func TestWireTypesMirrorDomain(t *testing.T) {
	for _, tc := range []struct {
		domain, wire any
		wireOnly     []string
	}{
		{registry.Server{}, apitypes.Server{}, nil},
		{registry.Tool{}, apitypes.Tool{}, nil},
		{registry.Registry{}, apitypes.Registry{}, nil},
		{routing.Capability{}, apitypes.Capability{}, nil},
		{gateway.Catalog{}, apitypes.Catalog{}, nil},
		{gateway.DiscoverRequest{}, apitypes.DiscoverRequest{}, []string{"sim_scope?"}},
		{gateway.DiscoverResult{}, apitypes.DiscoverResult{}, nil},
		{gateway.InvokeRequest{}, apitypes.InvokeRequest{}, []string{"sim_scope?"}},
		{gateway.InvokeResult{}, apitypes.InvokeResult{}, nil},
		{gateway.SetupStatus{}, apitypes.SetupStatus{}, nil},
		{model.Settings{}, apitypes.Settings{}, nil},
		{model.ModelProfile{}, apitypes.ModelProfile{}, nil},
		{model.PingModelResult{}, apitypes.PingModelResult{}, nil},
		{scope.ScopeOverview{}, apitypes.ScopeOverview{}, nil},
		{scope.ScopeRule{}, apitypes.ScopeRule{}, nil},
		{scope.ScopeSnippetResponse{}, apitypes.ScopeSnippetResponse{}, nil},
		{scope.SaveRepositoryRequest{}, apitypes.SaveRepositoryRequest{}, nil},
		{market.MarketSearchResult{}, apitypes.MarketSearchResult{}, nil},
		{market.InstallMarketRequest{}, apitypes.InstallMarketRequest{}, nil},
		{market.InstallMarketResult{}, apitypes.InstallMarketResult{}, nil},
		{maintenance.Status{}, apitypes.MaintenanceStatus{}, nil},
		{maintenance.RunDetails{}, apitypes.MaintenanceRunDetails{}, nil},
		{maintenance.AcceptanceSuite{}, apitypes.AcceptanceSuite{}, nil},
		{access.Principal{}, apitypes.Principal{}, nil},
		{activity.Event{}, apitypes.Event{}, nil},
		{activity.PulseBucket{}, apitypes.PulseBucket{}, nil},
	} {
		dt, wt := reflect.TypeOf(tc.domain), reflect.TypeOf(tc.wire)
		t.Run(dt.String(), func(t *testing.T) {
			domain := structFields(dt, map[reflect.Type]bool{})
			wire := structFields(wt, map[reflect.Type]bool{})
			for _, k := range tc.wireOnly {
				name, _, _ := strings.Cut(k, ":")
				if _, ok := wire[name]; !ok {
					t.Fatalf("wireOnly field %q is not on %s", name, wt)
				}
				delete(wire, name)
			}
			if got, want := fmt.Sprint(wire), fmt.Sprint(domain); got != want {
				t.Fatalf("%s no longer mirrors %s:\n wire:   %s\n domain: %s", wt, dt, got, want)
			}
		})
	}
}
