import type { Bootstrap, Event, Server, Tool } from "./types";

export type BlindSpotItem = {
  intent: string;
  status: string;
  actor: string;
  count: number;
  lastTime: string;
  lastEventId: string;
  errorSummary?: string;
};

export type TopToolItem = {
  id: string;
  name: string;
  serverId: string;
  serverName: string;
  readOnly: boolean;
  hits: number;
};

export type GatewayTelemetry = {
  sampleSize: number;
  discoverCount: number;
  invokeCount: number;
  agentCount: number;
  selectedCount: number;
  noMatchCount: number;
  needsContextCount: number;
  errorCount: number;
  hitRatePct: number | null;
  avgEligible: number | null;
  avgSelectedOnHit: number | null;
  compressionPct: number | null;
  p50DiscoverMs: number | null;
  p95DiscoverMs: number | null;
  avgInvokeMs: number | null;
  totalTokens: number;
  toolHitCounts: Record<string, number>;
  topTools: TopToolItem[];
  attentionItems: BlindSpotItem[];
};

export type ActorPolicyRow = {
  actor: string;
  reachableServers: number;
  totalEnabledTools: number;
  readOnlyTools: number;
  writeTools: number;
  byServer: {
    serverId: string;
    serverName: string;
    readOnly: number;
    write: number;
  }[];
};

function percentile(sortedAsc: number[], p: number): number | null {
  if (sortedAsc.length === 0) return null;
  const idx = Math.min(
    sortedAsc.length - 1,
    Math.max(0, Math.ceil((p / 100) * sortedAsc.length) - 1),
  );
  return sortedAsc[idx];
}

export function computeGatewayTelemetry(state: Bootstrap): GatewayTelemetry {
  const events: Event[] = state.events || [];
  const servers = state.registry.servers;
  const tools = state.registry.tools;

  let discoverCount = 0;
  let invokeCount = 0;
  let agentCount = 0;

  let selectedCount = 0;
  let noMatchCount = 0;
  let needsContextCount = 0;
  let errorCount = 0;

  let eligibleSum = 0;
  let eligibleSamples = 0;
  let selectedToolsSum = 0;
  let selectedSamples = 0;
  let totalTokens = 0;

  const discoverDurations: number[] = [];
  const invokeDurations: number[] = [];
  const toolHitCounts: Record<string, number> = {};
  const blindSpotMap = new Map<string, BlindSpotItem>();

  for (const ev of events) {
    if (ev.kind === "discover") {
      discoverCount++;
      if (typeof ev.duration_ms === "number" && ev.duration_ms >= 0) {
        discoverDurations.push(ev.duration_ms);
      }
    } else if (ev.kind === "invoke") {
      invokeCount++;
      if (typeof ev.duration_ms === "number" && ev.duration_ms >= 0) {
        invokeDurations.push(ev.duration_ms);
      }
    } else if (ev.kind === "agent") {
      agentCount++;
    }

    const isErr = ev.status === "error" || ev.status === "failed";
    if (isErr) {
      errorCount++;
    } else if (ev.status === "selected") {
      selectedCount++;
    } else if (ev.status === "no_match") {
      noMatchCount++;
    } else if (ev.status === "needs_context") {
      needsContextCount++;
    }

    const detail = ev.detail;
    if (detail && typeof detail === "object") {
      if (typeof detail.eligible === "number" && detail.eligible > 0) {
        eligibleSum += detail.eligible;
        eligibleSamples++;
      }
      if (typeof detail.prompt_tokens === "number") {
        totalTokens += detail.prompt_tokens + (detail.output_tokens || 0);
      }
      if (Array.isArray(detail.tools)) {
        if (ev.status === "selected") {
          selectedToolsSum += detail.tools.length;
          selectedSamples++;
        }
        for (const item of detail.tools) {
          const id =
            typeof item === "string" ? item : item?.id || item?.tool_id || "";
          if (id) {
            toolHitCounts[id] = (toolHitCounts[id] || 0) + 1;
          }
        }
      }
    }

    if (
      isErr ||
      ev.status === "no_match" ||
      ev.status === "needs_context"
    ) {
      const key = `${ev.status}::${ev.title.trim()}`;
      const existing = blindSpotMap.get(key);
      if (existing) {
        existing.count += 1;
      } else {
        blindSpotMap.set(key, {
          intent: ev.title,
          status: ev.status,
          actor: ev.actor,
          count: 1,
          lastTime: ev.time,
          lastEventId: ev.id,
          errorSummary: detail?.error
            ? String(detail.error).split("\n")[0].slice(0, 120)
            : detail?.question
              ? String(detail.question)
              : undefined,
        });
      }
    }
  }

  discoverDurations.sort((a, b) => a - b);

  const hitRatePct =
    discoverCount > 0 ? (selectedCount / discoverCount) * 100 : null;
  const avgEligible =
    eligibleSamples > 0 ? eligibleSum / eligibleSamples : null;
  const avgSelectedOnHit =
    selectedSamples > 0 ? selectedToolsSum / selectedSamples : null;
  const compressionPct =
    avgEligible && avgEligible > 0 && avgSelectedOnHit !== null
      ? Math.max(0, (1 - avgSelectedOnHit / avgEligible) * 100)
      : null;

  const avgInvokeMs =
    invokeDurations.length > 0
      ? Math.round(
          invokeDurations.reduce((a, b) => a + b, 0) / invokeDurations.length,
        )
      : null;

  const topTools: TopToolItem[] = Object.entries(toolHitCounts)
    .map(([id, hits]) => {
      const regTool = tools.find((t) => t.id === id);
      const serverId = regTool?.server_id || id.split(".")[0] || "";
      const srv = servers.find((s) => s.id === serverId);
      return {
        id,
        name: regTool?.name || id.split(".").slice(1).join(".") || id,
        serverId,
        serverName: srv?.name || serverId,
        readOnly: regTool ? regTool.read_only : true,
        hits,
      };
    })
    .sort((a, b) => b.hits - a.hits)
    .slice(0, 6);

  // Sort attention items: errors first, then higher frequency, then recency
  const attentionItems = Array.from(blindSpotMap.values())
    .sort((a, b) => {
      const aErr = a.status === "error" || a.status === "failed" ? 1 : 0;
      const bErr = b.status === "error" || b.status === "failed" ? 1 : 0;
      if (aErr !== bErr) return bErr - aErr;
      if (b.count !== a.count) return b.count - a.count;
      return b.lastTime.localeCompare(a.lastTime);
    })
    .slice(0, 7);

  return {
    sampleSize: events.length,
    discoverCount,
    invokeCount,
    agentCount,
    selectedCount,
    noMatchCount,
    needsContextCount,
    errorCount,
    hitRatePct,
    avgEligible,
    avgSelectedOnHit,
    compressionPct,
    p50DiscoverMs: percentile(discoverDurations, 50),
    p95DiscoverMs: percentile(discoverDurations, 95),
    avgInvokeMs,
    totalTokens,
    toolHitCounts,
    topTools,
    attentionItems,
  };
}

export function computeActorMatrix(
  servers: Server[],
  tools: Tool[],
  sharedActor?: string,
): ActorPolicyRow[] {
  const realActiveServers = servers.filter((s) => !s.demo && s.enabled);
  const activeServerIds = new Set(realActiveServers.map((s) => s.id));
  const enabledRealTools = tools.filter(
    (t) => t.enabled && activeServerIds.has(t.server_id),
  );

  const actorSet = new Set<string>(["developer", "observer"]);
  if (sharedActor) actorSet.add(sharedActor);
  for (const t of tools) {
    if (!activeServerIds.has(t.server_id)) continue;
    for (const a of t.actors || []) {
      if (a) actorSet.add(a);
    }
  }

  return Array.from(actorSet).map((actor) => {
    const allowed = enabledRealTools.filter(
      (t) => t.actors.includes("*") || t.actors.includes(actor),
    );
    const readOnlyTools = allowed.filter((t) => t.read_only).length;
    const writeTools = allowed.length - readOnlyTools;
    const byServer = realActiveServers
      .map((srv) => {
        const srvAllowed = allowed.filter((t) => t.server_id === srv.id);
        const ro = srvAllowed.filter((t) => t.read_only).length;
        return {
          serverId: srv.id,
          serverName: srv.name,
          readOnly: ro,
          write: srvAllowed.length - ro,
        };
      })
      .filter((row) => row.readOnly + row.write > 0);

    return {
      actor,
      reachableServers: byServer.length,
      totalEnabledTools: allowed.length,
      readOnlyTools,
      writeTools,
      byServer,
    };
  });
}

export function formatCompactTokens(tokens: number): string {
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(2)}M`;
  if (tokens >= 1_000) return `${(tokens / 1_000).toFixed(1)}k`;
  return String(tokens);
}
