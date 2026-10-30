import { useEffect, useState } from "react";
import { api, pretty } from "./types";

export function AcceptanceEditor({ serverID, native }: { serverID: string; native: boolean }) {
  const [value, setValue] = useState("");
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let active = true;
    setValue(""); setError(""); setSaved(false);
    api(`/maintenance/sources/${encodeURIComponent(serverID)}/acceptance`)
      .then(data => { if (active) setValue(pretty(data)); })
      .catch(error => { if (active) setError(error.message); });
    return () => { active = false; };
  }, [serverID]);
  function example() {
    const id = `${serverID}.get_status`;
    setValue(pretty({
      server_id: serverID,
      contracts: [{
        name: "状态查询返回值", tool_id: id, arguments: {},
        ...(native ? { mcp_result: { status: "ok" } } : {
          exchanges: [{ method: "GET", path: "/status", status: 200, response: { status: "ok" } }],
        }),
        expected: { status: "ok" },
      }],
      routing: [{ name: "状态查询路由", intent: "查询服务状态 status", mode: "retrieval", expected: [id] }],
    }));
    setSaved(false);
  }
  async function save() {
    setBusy(true); setError(""); setSaved(false);
    try {
      await api(`/maintenance/sources/${encodeURIComponent(serverID)}/acceptance`, {
        method: "PUT", body: pretty(JSON.parse(value)),
      });
      setSaved(true);
    } catch (error) { setError((error as Error).message); }
    finally { setBusy(false); }
  }
  return <details className="maintenance-acceptance-editor">
    <summary>独立验收用例</summary>
    <p className="maintenance-help">用例由你维护，不会发送给 Pi。每个工具需要成功调用的契约用例；启用的工具还需要路由用例。保存后重新检查来源，新的验收结果通过后才能发布。</p>
    <p className="maintenance-help">HTTP 用例验证实际方法、编码后的路径、query、请求体和返回值；使用本地模拟响应，不调用真实写接口。路由模式可选 retrieval 或 model，后者使用当前配置的路由模型。</p>
    <textarea aria-label="独立验收用例 JSON" className="text-input mono" rows={15} value={value} onChange={event => { setValue(event.target.value); setSaved(false); }} spellCheck={false} />
    {error && <p className="maintenance-message error" role="alert">{error}</p>}
    {saved && <p className="maintenance-help" role="status">验收用例已保存。重新检查来源后生效。</p>}
    <div className="maintenance-actions"><button className="button secondary" type="button" onClick={example}>填入示例</button><button className="button primary" type="button" disabled={busy || !value} onClick={() => void save()}>保存验收用例</button></div>
  </details>;
}
