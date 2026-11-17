package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
)

// BlindSpotItem aggregates repeated routing misses, context requests, or errors for the overview panel.
type BlindSpotItem struct {
	Intent       string
	Status       string
	Actor        string
	Count        int
	LastTime     time.Time
	LastEventID  string
	ErrorSummary string
}

// TopToolItem summarizes the most frequently selected tools in recent events.
type TopToolItem struct {
	ID         string
	Name       string
	ServerID   string
	ServerName string
	ReadOnly   bool
	Hits       int
}

// GatewayTelemetry holds aggregated runtime metrics derived from a bootstrap snapshot.
type GatewayTelemetry struct {
	SampleSize        int
	DiscoverCount     int
	InvokeCount       int
	AgentCount        int
	SelectedCount     int
	NoMatchCount      int
	NeedsContextCount int
	ErrorCount        int
	HitRatePct        *float64
	SuccessRatePct    *float64
	AvgEligible       *float64
	AvgSelectedOnHit  *float64
	CompressionPct    *float64
	P50DiscoverMs     *int64
	P95DiscoverMs     *int64
	AvgInvokeMs       *int64
	TotalTokens       int
	ToolHitCounts     map[string]int
	TopTools          []TopToolItem
	AttentionItems    []BlindSpotItem
	PulseBuckets      []apitypes.PulseBucket
}

// ActorServerCount counts reachable read-only and write tools per service for one actor.
type ActorServerCount struct {
	ServerID   string
	ServerName string
	ReadOnly   int
	Write      int
}

// ActorPolicyRow summarizes the effective service/tool surface for one actor.
type ActorPolicyRow struct {
	Actor             string
	ReachableServers  int
	TotalEnabledTools int
	ReadOnlyTools     int
	WriteTools        int
	ByServer          []ActorServerCount
}

func percentileInt64(sortedAsc []int64, p float64) *int64 {
	if len(sortedAsc) == 0 {
		return nil
	}
	idx := int(math.Ceil((p/100.0)*float64(len(sortedAsc)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sortedAsc) {
		idx = len(sortedAsc) - 1
	}
	v := sortedAsc[idx]
	return &v
}

// ComputeGatewayTelemetry computes real-time overview metrics from the bootstrap snapshot.
func ComputeGatewayTelemetry(state apitypes.Bootstrap) GatewayTelemetry {
	events := state.Events
	servers := state.Registry.Servers
	tools := state.Registry.Tools

	var (
		discoverCount     int
		invokeCount       int
		agentCount        int
		selectedCount     int
		noMatchCount      int
		needsContextCount int
		errorCount        int

		eligibleSum      float64
		eligibleSamples  int
		selectedToolsSum float64
		selectedSamples  int
		totalTokens      int
	)

	discoverDurations := make([]int64, 0, len(events))
	invokeDurations := make([]int64, 0, len(events))
	toolHitCounts := make(map[string]int)
	blindSpotMap := make(map[string]*BlindSpotItem)

	toolByID := make(map[string]apitypes.Tool, len(tools))
	for _, t := range tools {
		toolByID[t.ID] = t
	}
	serverByID := make(map[string]apitypes.Server, len(servers))
	for _, s := range servers {
		serverByID[s.ID] = s
	}

	for _, ev := range events {
		switch ev.Kind {
		case "discover":
			discoverCount++
			if ev.DurationMS >= 0 {
				discoverDurations = append(discoverDurations, ev.DurationMS)
			}
		case "invoke":
			invokeCount++
			if ev.DurationMS >= 0 {
				invokeDurations = append(invokeDurations, ev.DurationMS)
			}
		case "agent":
			agentCount++
		}

		isErr := ev.Status == "error" || ev.Status == "failed"
		if isErr {
			errorCount++
		} else {
			switch ev.Status {
			case "selected":
				selectedCount++
			case "no_match":
				noMatchCount++
			case "needs_context":
				needsContextCount++
			}
		}

		var detailMap map[string]any
		if len(ev.Detail) > 0 {
			_ = json.Unmarshal(ev.Detail, &detailMap)
		}
		if detailMap != nil {
			if elig, ok := jsonFloat(detailMap["eligible"]); ok && elig > 0 {
				eligibleSum += elig
				eligibleSamples++
			}
			if pt, ok := jsonFloat(detailMap["prompt_tokens"]); ok {
				ot, _ := jsonFloat(detailMap["output_tokens"])
				totalTokens += int(pt + ot)
			}
			if rawTools, ok := detailMap["tools"].([]any); ok {
				if ev.Status == "selected" {
					selectedToolsSum += float64(len(rawTools))
					selectedSamples++
				}
				for _, item := range rawTools {
					var id string
					switch v := item.(type) {
					case string:
						id = v
					case map[string]any:
						if s, ok := v["id"].(string); ok && s != "" {
							id = s
						} else if s, ok := v["tool_id"].(string); ok && s != "" {
							id = s
						}
					}
					if id != "" {
						toolHitCounts[id]++
					}
				}
			}
		}

		if isErr || ev.Status == "no_match" || ev.Status == "needs_context" {
			key := ev.Status + "::" + strings.TrimSpace(ev.Title)
			if existing, ok := blindSpotMap[key]; ok {
				existing.Count++
			} else {
				var errSummary string
				if detailMap != nil {
					if rawErr, ok := detailMap["error"]; ok && rawErr != nil {
						firstLine := strings.SplitN(fmt.Sprint(rawErr), "\n", 2)[0]
						errSummary = truncateRunes(firstLine, 120)
					} else if rawQ, ok := detailMap["question"]; ok && rawQ != nil {
						errSummary = fmt.Sprint(rawQ)
					}
				}
				blindSpotMap[key] = &BlindSpotItem{
					Intent:       ev.Title,
					Status:       ev.Status,
					Actor:        ev.Actor,
					Count:        1,
					LastTime:     ev.Time,
					LastEventID:  ev.ID,
					ErrorSummary: errSummary,
				}
			}
		}
	}

	slices.Sort(discoverDurations)

	var hitRatePct *float64
	if discoverCount > 0 {
		v := (float64(selectedCount) / float64(discoverCount)) * 100
		hitRatePct = &v
	}
	var successRatePct *float64
	if len(events) > 0 {
		v := (float64(len(events)-errorCount) / float64(len(events))) * 100
		successRatePct = &v
	}
	var avgEligible *float64
	if eligibleSamples > 0 {
		v := eligibleSum / float64(eligibleSamples)
		avgEligible = &v
	}
	var avgSelectedOnHit *float64
	if selectedSamples > 0 {
		v := selectedToolsSum / float64(selectedSamples)
		avgSelectedOnHit = &v
	}
	var compressionPct *float64
	if avgEligible != nil && *avgEligible > 0 && avgSelectedOnHit != nil {
		v := math.Max(0, (1.0 - *avgSelectedOnHit / *avgEligible)*100)
		compressionPct = &v
	}
	var avgInvokeMs *int64
	if len(invokeDurations) > 0 {
		var sum int64
		for _, d := range invokeDurations {
			sum += d
		}
		v := int64(math.Round(float64(sum) / float64(len(invokeDurations))))
		avgInvokeMs = &v
	}

	topTools := make([]TopToolItem, 0, len(toolHitCounts))
	for id, hits := range toolHitCounts {
		regTool, hasTool := toolByID[id]
		serverID := regTool.ServerID
		if serverID == "" {
			if parts := strings.SplitN(id, ".", 2); len(parts) > 0 {
				serverID = parts[0]
			}
		}
		srv := serverByID[serverID]
		name := regTool.Name
		if name == "" {
			if parts := strings.SplitN(id, ".", 2); len(parts) == 2 {
				name = parts[1]
			} else {
				name = id
			}
		}
		srvName := srv.Name
		if srvName == "" {
			srvName = serverID
		}
		readOnly := true
		if hasTool {
			readOnly = regTool.ReadOnly
		}
		topTools = append(topTools, TopToolItem{
			ID:         id,
			Name:       name,
			ServerID:   serverID,
			ServerName: srvName,
			ReadOnly:   readOnly,
			Hits:       hits,
		})
	}
	sort.Slice(topTools, func(i, j int) bool {
		if topTools[i].Hits != topTools[j].Hits {
			return topTools[i].Hits > topTools[j].Hits
		}
		return topTools[i].ID < topTools[j].ID
	})
	if len(topTools) > 6 {
		topTools = topTools[:6]
	}

	attentionItems := make([]BlindSpotItem, 0, len(blindSpotMap))
	for _, item := range blindSpotMap {
		attentionItems = append(attentionItems, *item)
	}
	sort.Slice(attentionItems, func(i, j int) bool {
		aErr := attentionItems[i].Status == "error" || attentionItems[i].Status == "failed"
		bErr := attentionItems[j].Status == "error" || attentionItems[j].Status == "failed"
		if aErr != bErr {
			return aErr
		}
		if attentionItems[i].Count != attentionItems[j].Count {
			return attentionItems[i].Count > attentionItems[j].Count
		}
		return attentionItems[i].LastTime.After(attentionItems[j].LastTime)
	})
	if len(attentionItems) > 7 {
		attentionItems = attentionItems[:7]
	}

	pulseBuckets := computePulseBuckets(state, time.Now())

	return GatewayTelemetry{
		SampleSize:        len(events),
		DiscoverCount:     discoverCount,
		InvokeCount:       invokeCount,
		AgentCount:        agentCount,
		SelectedCount:     selectedCount,
		NoMatchCount:      noMatchCount,
		NeedsContextCount: needsContextCount,
		ErrorCount:        errorCount,
		HitRatePct:        hitRatePct,
		SuccessRatePct:    successRatePct,
		AvgEligible:       avgEligible,
		AvgSelectedOnHit:  avgSelectedOnHit,
		CompressionPct:    compressionPct,
		P50DiscoverMs:     percentileInt64(discoverDurations, 50),
		P95DiscoverMs:     percentileInt64(discoverDurations, 95),
		AvgInvokeMs:       avgInvokeMs,
		TotalTokens:       totalTokens,
		ToolHitCounts:     toolHitCounts,
		TopTools:          topTools,
		AttentionItems:    attentionItems,
		PulseBuckets:      pulseBuckets,
	}
}

func computePulseBuckets(state apitypes.Bootstrap, now time.Time) []apitypes.PulseBucket {
	const (
		bucketCount = 48
		slotDur     = 15 * time.Minute
	)
	if len(state.Pulse) == bucketCount {
		out := make([]apitypes.PulseBucket, bucketCount)
		copy(out, state.Pulse)
		return out
	}

	end := now.Truncate(slotDur).Add(slotDur)
	start := end.Add(-time.Duration(bucketCount) * slotDur)
	out := make([]apitypes.PulseBucket, bucketCount)
	for i := range out {
		bStart := start.Add(time.Duration(i) * slotDur)
		out[i] = apitypes.PulseBucket{
			Start: bStart,
			End:   bStart.Add(slotDur),
		}
	}
	for _, ev := range state.Events {
		if ev.Time.Before(start) || !ev.Time.Before(end) {
			continue
		}
		idx := int(ev.Time.Sub(start) / slotDur)
		if idx < 0 || idx >= bucketCount {
			continue
		}
		out[idx].Total++
		if ev.Status == "error" || ev.Status == "failed" {
			out[idx].Error++
		}
	}
	return out
}

// ComputeActorMatrix computes per-actor reachable services and read/write tool counts.
func ComputeActorMatrix(servers []apitypes.Server, tools []apitypes.Tool, sharedActor string) []ActorPolicyRow {
	realActiveServers := make([]apitypes.Server, 0, len(servers))
	activeServerIDs := make(map[string]bool, len(servers))
	for _, s := range servers {
		if !s.Demo && s.Enabled {
			realActiveServers = append(realActiveServers, s)
			activeServerIDs[s.ID] = true
		}
	}

	enabledRealTools := make([]apitypes.Tool, 0, len(tools))
	for _, t := range tools {
		if t.Enabled && activeServerIDs[t.ServerID] {
			enabledRealTools = append(enabledRealTools, t)
		}
	}

	actorOrder := []string{"developer", "observer"}
	actorSeen := map[string]bool{"developer": true, "observer": true}
	if sharedActor != "" && !actorSeen[sharedActor] {
		actorOrder = append(actorOrder, sharedActor)
		actorSeen[sharedActor] = true
	}
	for _, t := range tools {
		if !activeServerIDs[t.ServerID] {
			continue
		}
		for _, a := range t.Actors {
			if a != "" && !actorSeen[a] {
				actorOrder = append(actorOrder, a)
				actorSeen[a] = true
			}
		}
	}

	rows := make([]ActorPolicyRow, 0, len(actorOrder))
	for _, actor := range actorOrder {
		var allowed []apitypes.Tool
		readOnly := 0
		for _, t := range enabledRealTools {
			if slices.Contains(t.Actors, "*") || slices.Contains(t.Actors, actor) {
				allowed = append(allowed, t)
				if t.ReadOnly {
					readOnly++
				}
			}
		}
		writeTools := len(allowed) - readOnly
		byServer := make([]ActorServerCount, 0, len(realActiveServers))
		for _, srv := range realActiveServers {
			srvRO, srvTotal := 0, 0
			for _, t := range allowed {
				if t.ServerID == srv.ID {
					srvTotal++
					if t.ReadOnly {
						srvRO++
					}
				}
			}
			if srvTotal > 0 {
				byServer = append(byServer, ActorServerCount{
					ServerID:   srv.ID,
					ServerName: srv.Name,
					ReadOnly:   srvRO,
					Write:      srvTotal - srvRO,
				})
			}
		}
		rows = append(rows, ActorPolicyRow{
			Actor:             actor,
			ReachableServers:  len(byServer),
			TotalEnabledTools: len(allowed),
			ReadOnlyTools:     readOnly,
			WriteTools:        writeTools,
			ByServer:          byServer,
		})
	}
	return rows
}

func FormatCompactTokens(tokens int) string {
	if tokens >= 1_000_000 {
		return fmt.Sprintf("%.2fM", float64(tokens)/1_000_000)
	}
	if tokens >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1_000)
	}
	return fmt.Sprintf("%d", tokens)
}

func ServerProtocolLabel(s apitypes.Server) string {
	if s.Kind == "http" {
		return "HTTP API"
	}
	if s.Transport == "stdio" {
		return "MCP · stdio"
	}
	return "MCP · HTTP"
}

func ServerTargetText(s apitypes.Server) string {
	if s.Kind == "mcp" && s.Transport == "stdio" {
		parts := make([]string, 0, 1+len(s.Args))
		if s.Command != "" {
			parts = append(parts, s.Command)
		}
		parts = append(parts, s.Args...)
		if joined := strings.TrimSpace(strings.Join(parts, " ")); joined != "" {
			return joined
		}
		return "—"
	}
	if s.Endpoint != "" {
		return s.Endpoint
	}
	return "—"
}

func ExampleFromSchema(schema map[string]any) map[string]any {
	out := map[string]any{}
	if schema == nil {
		return out
	}
	props, _ := schema["properties"].(map[string]any)
	var reqKeys []string
	switch raw := schema["required"].(type) {
	case []string:
		reqKeys = raw
	case []any:
		for _, item := range raw {
			if s, ok := item.(string); ok {
				reqKeys = append(reqKeys, s)
			}
		}
	}
	for _, key := range reqKeys {
		p, _ := props[key].(map[string]any)
		if p == nil {
			out[key] = ""
			continue
		}
		if def, ok := p["default"]; ok && def != nil {
			out[key] = def
			continue
		}
		if ex, ok := p["examples"].([]any); ok && len(ex) > 0 {
			out[key] = ex[0]
			continue
		}
		if en, ok := p["enum"].([]any); ok && len(en) > 0 {
			out[key] = en[0]
			continue
		}
		switch p["type"] {
		case "integer", "number":
			if minVal, ok := p["minimum"]; ok && minVal != nil {
				out[key] = minVal
			} else {
				out[key] = 1
			}
		case "boolean":
			out[key] = false
		case "object":
			out[key] = map[string]any{}
		case "array":
			out[key] = []any{}
		default:
			out[key] = ""
		}
	}
	return out
}

func FormatTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("01-02 15:04:05")
}

func PrettyJSON(v any) string {
	if v == nil {
		return "{}"
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func jsonFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

func formatAPIError(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *apiclient.APIError
	if errors.As(err, &apiErr) && apiErr.Detail != "" {
		return apiErr.Error() + "\n" + apiErr.Detail
	}
	return err.Error()
}

func splitAPIError(err error) (string, string) {
	var apiErr *apiclient.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Error(), apiErr.Detail
	}
	return err.Error(), ""
}
