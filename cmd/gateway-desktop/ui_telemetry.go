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
	acc := newTelemetryAccumulator(state)
	for _, ev := range state.Events {
		acc.addEvent(ev)
	}
	return acc.summarize(state)
}

// telemetryAccumulator folds each bootstrap event into the counters used by the
// Overview page so the per-event logic stays in small, testable helpers.
type telemetryAccumulator struct {
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

	discoverDurations []int64
	invokeDurations   []int64
	toolHitCounts     map[string]int
	blindSpotMap      map[string]*BlindSpotItem
	toolByID          map[string]apitypes.Tool
	serverByID        map[string]apitypes.Server
}

func newTelemetryAccumulator(state apitypes.Bootstrap) *telemetryAccumulator {
	events := state.Events
	servers := state.Registry.Servers
	tools := state.Registry.Tools
	acc := &telemetryAccumulator{
		discoverDurations: make([]int64, 0, len(events)),
		invokeDurations:   make([]int64, 0, len(events)),
		toolHitCounts:     make(map[string]int),
		blindSpotMap:      make(map[string]*BlindSpotItem),
		toolByID:          make(map[string]apitypes.Tool, len(tools)),
		serverByID:        make(map[string]apitypes.Server, len(servers)),
	}
	for _, t := range tools {
		acc.toolByID[t.ID] = t
	}
	for _, s := range servers {
		acc.serverByID[s.ID] = s
	}
	return acc
}

func (a *telemetryAccumulator) addEvent(ev apitypes.Event) {
	switch ev.Kind {
	case "discover":
		a.discoverCount++
		if ev.DurationMS >= 0 {
			a.discoverDurations = append(a.discoverDurations, ev.DurationMS)
		}
	case "invoke":
		a.invokeCount++
		if ev.DurationMS >= 0 {
			a.invokeDurations = append(a.invokeDurations, ev.DurationMS)
		}
	case "agent":
		a.agentCount++
	}

	a.recordStatus(ev)

	if detailMap := parseTelemetryDetail(ev.Detail); detailMap != nil {
		a.recordDetailMetrics(ev, detailMap)
		a.recordBlindSpot(ev, detailMap)
	} else {
		a.recordBlindSpot(ev, nil)
	}
}

func (a *telemetryAccumulator) recordStatus(ev apitypes.Event) {
	if ev.Status == "error" || ev.Status == "failed" {
		a.errorCount++
		return
	}
	switch ev.Status {
	case "selected":
		a.selectedCount++
	case "no_match":
		a.noMatchCount++
	case "needs_context":
		a.needsContextCount++
	}
}

func (a *telemetryAccumulator) recordDetailMetrics(ev apitypes.Event, detailMap map[string]any) {
	if elig, ok := jsonFloat(detailMap["eligible"]); ok && elig > 0 {
		a.eligibleSum += elig
		a.eligibleSamples++
	}
	if pt, ok := jsonFloat(detailMap["prompt_tokens"]); ok {
		ot, _ := jsonFloat(detailMap["output_tokens"])
		a.totalTokens += int(pt + ot)
	}
	rawTools, ok := detailMap["tools"].([]any)
	if !ok {
		return
	}
	if ev.Status == "selected" {
		a.selectedToolsSum += float64(len(rawTools))
		a.selectedSamples++
	}
	for _, item := range rawTools {
		if id := telemetryToolID(item); id != "" {
			a.toolHitCounts[id]++
		}
	}
}

func (a *telemetryAccumulator) recordBlindSpot(ev apitypes.Event, detailMap map[string]any) {
	if ev.Status != "error" && ev.Status != "failed" && ev.Status != "no_match" && ev.Status != "needs_context" {
		return
	}
	key := ev.Status + "::" + strings.TrimSpace(ev.Title)
	if existing, ok := a.blindSpotMap[key]; ok {
		existing.Count++
		return
	}
	a.blindSpotMap[key] = &BlindSpotItem{
		Intent:       ev.Title,
		Status:       ev.Status,
		Actor:        ev.Actor,
		Count:        1,
		LastTime:     ev.Time,
		LastEventID:  ev.ID,
		ErrorSummary: telemetryErrorSummary(detailMap),
	}
}

func (a *telemetryAccumulator) summarize(state apitypes.Bootstrap) GatewayTelemetry {
	slices.Sort(a.discoverDurations)

	out := GatewayTelemetry{
		SampleSize:        len(state.Events),
		DiscoverCount:     a.discoverCount,
		InvokeCount:       a.invokeCount,
		AgentCount:        a.agentCount,
		SelectedCount:     a.selectedCount,
		NoMatchCount:      a.noMatchCount,
		NeedsContextCount: a.needsContextCount,
		ErrorCount:        a.errorCount,
		HitRatePct:        telemetryPercent(a.selectedCount, a.discoverCount),
		SuccessRatePct:    telemetrySuccessRate(len(state.Events), a.errorCount),
		AvgEligible:       telemetryAverage(a.eligibleSum, a.eligibleSamples),
		AvgSelectedOnHit:  telemetryAverage(a.selectedToolsSum, a.selectedSamples),
		P50DiscoverMs:     percentileInt64(a.discoverDurations, 50),
		P95DiscoverMs:     percentileInt64(a.discoverDurations, 95),
		AvgInvokeMs:       telemetryAvgInt64(a.invokeDurations),
		TotalTokens:       a.totalTokens,
		ToolHitCounts:     a.toolHitCounts,
	}
	out.CompressionPct = telemetryCompression(out.AvgEligible, out.AvgSelectedOnHit)
	out.TopTools = buildTopTools(a.toolHitCounts, a.toolByID, a.serverByID)
	out.AttentionItems = buildAttentionItems(a.blindSpotMap)
	out.PulseBuckets = computePulseBuckets(state, time.Now())
	return out
}

func parseTelemetryDetail(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

func telemetryToolID(item any) string {
	switch v := item.(type) {
	case string:
		return v
	case map[string]any:
		if s, ok := v["id"].(string); ok && s != "" {
			return s
		}
		if s, ok := v["tool_id"].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func telemetryErrorSummary(m map[string]any) string {
	if m == nil {
		return ""
	}
	if rawErr, ok := m["error"]; ok && rawErr != nil {
		firstLine := strings.SplitN(fmt.Sprint(rawErr), "\n", 2)[0]
		return truncateRunes(firstLine, 120)
	}
	if rawQ, ok := m["question"]; ok && rawQ != nil {
		return fmt.Sprint(rawQ)
	}
	return ""
}

func telemetryPercent(numerator, denominator int) *float64 {
	if denominator <= 0 {
		return nil
	}
	v := (float64(numerator) / float64(denominator)) * 100
	return &v
}

func telemetrySuccessRate(total, errors int) *float64 {
	if total <= 0 {
		return nil
	}
	v := (float64(total-errors) / float64(total)) * 100
	return &v
}

func telemetryAverage(sum float64, samples int) *float64 {
	if samples <= 0 {
		return nil
	}
	v := sum / float64(samples)
	return &v
}

func telemetryCompression(avgEligible, avgSelected *float64) *float64 {
	if avgEligible == nil || *avgEligible <= 0 || avgSelected == nil {
		return nil
	}
	v := math.Max(0, (1.0-*avgSelected/(*avgEligible))*100)
	return &v
}

func telemetryAvgInt64(vals []int64) *int64 {
	if len(vals) == 0 {
		return nil
	}
	var sum int64
	for _, d := range vals {
		sum += d
	}
	v := int64(math.Round(float64(sum) / float64(len(vals))))
	return &v
}

func buildTopTools(
	hits map[string]int,
	toolByID map[string]apitypes.Tool,
	serverByID map[string]apitypes.Server,
) []TopToolItem {
	topTools := make([]TopToolItem, 0, len(hits))
	for id, hitCount := range hits {
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
			Hits:       hitCount,
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
	return topTools
}

func buildAttentionItems(blindSpotMap map[string]*BlindSpotItem) []BlindSpotItem {
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
	return attentionItems
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
