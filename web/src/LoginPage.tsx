import { useState, type FormEvent } from "react";
import { Loader2, ShieldCheck } from "lucide-react";
import { api } from "./types";
import { AlertBox, GatewayLogo } from "./ui";

export function LoginPage({ onLogin }: { onLogin: () => Promise<unknown> }) {
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function login(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api("/auth/login", {
        method: "POST",
        body: JSON.stringify({ token }),
      });
      setToken("");
      await onLogin();
    } catch (e: any) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="center-screen">
      <form className="auth-card" onSubmit={login}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <span className="sail-brand-mark">
            <GatewayLogo size={17} />
          </span>
          <div>
            <h1 style={{ fontSize: 17, fontWeight: 650, color: "var(--ink-strong)" }}>
              登录 Gateway Lab
            </h1>
            <p style={{ fontSize: 12.5, color: "var(--ink-muted)" }}>
              输入管理员访问令牌进入共享工作区
            </p>
          </div>
        </div>

        <label className="field" htmlFor="gateway-token">
          <span className="field-label">访问令牌（GATEWAY_TOKEN）</span>
          <input
            id="gateway-token"
            className="input mono"
            type="password"
            autoComplete="off"
            autoFocus
            required
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
        </label>

        {error && <AlertBox tone="error" title={error} />}

        <button
          type="submit"
          className="btn btn-primary btn-lg"
          disabled={busy || !token.trim()}
        >
          {busy ? <Loader2 size={15} className="spin" /> : <ShieldCheck size={15} />}
          验证并进入控制台
        </button>

        <p className="field-hint">
          令牌不保存在浏览器本地存储中，会话有效期为 8 小时。
        </p>
      </form>
    </div>
  );
}
