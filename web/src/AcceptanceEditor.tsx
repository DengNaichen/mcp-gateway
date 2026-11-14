import { useEffect, useState } from "react";
import { api, pretty } from "./types";
import { AlertBox } from "./ui";

export function AcceptanceEditor({
  serverID,
  native,
}: {
  serverID: string;
  native: boolean;
}) {
  const [value, setValue] = useState("");
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let active = true;
    setValue("");
    setError("");
    setSaved(false);
    api(`/maintenance/sources/${encodeURIComponent(serverID)}/acceptance`)
      .then((data) => {
        if (active) setValue(pretty(data));
      })
      .catch((err) => {
        if (active) setError(err.message);
      });
    return () => {
      active = false;
    };
  }, [serverID]);

  function fillExample() {
    const id = `${serverID}.get_status`;
    setValue(
      pretty({
        server_id: serverID,
        contracts: [
          {
            name: "状态查询返回值",
            tool_id: id,
            arguments: {},
            ...(native
              ? { mcp_result: { status: "ok" } }
              : {
                  exchanges: [
                    {
                      method: "GET",
                      path: "/status",
                      status: 200,
                      response: { status: "ok" },
                    },
                  ],
                }),
            expected: { status: "ok" },
          },
        ],
        routing: [
          {
            name: "状态查询路由",
            intent: "查询服务状态 status",
            expected: [id],
          },
        ],
      }),
    );
    setSaved(false);
  }

  async function save() {
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      await api(
        `/maintenance/sources/${encodeURIComponent(serverID)}/acceptance`,
        {
          method: "PUT",
          body: pretty(JSON.parse(value)),
        },
      );
      setSaved(true);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <details className="sail-details">
      <summary>独立验收用例（发布前契约与路由校验）</summary>
      <div className="form-stack">
        <p className="field-hint">
          用例独立保存在网关数据库中，不会发送给 Pi。每个工具需要成功契约覆盖，已启用或新增工具还需路由用例覆盖。保存后重新运行检查生效。
        </p>
        <textarea
          aria-label="独立验收用例 JSON"
          className="textarea mono"
          rows={11}
          value={value}
          onChange={(e) => {
            setValue(e.target.value);
            setSaved(false);
          }}
          spellCheck={false}
        />
        {error && <AlertBox tone="error" title={error} />}
        {saved && (
          <AlertBox tone="success" title="验收用例已保存，重新运行检查后生效。" />
        )}
        <div style={{ display: "flex", gap: 8 }}>
          <button type="button" className="btn btn-sm" onClick={fillExample}>
            填入模板示例
          </button>
          <button
            type="button"
            className="btn btn-primary btn-sm"
            disabled={busy || !value}
            onClick={() => void save()}
          >
            保存验收用例
          </button>
        </div>
      </div>
    </details>
  );
}
