import { useState, type FormEvent } from "react";
import { ShieldCheck, Loader2 } from "lucide-react";
import { api } from "./types";

export function LoginPage({ onLogin }: { onLogin: () => Promise<unknown> }) {
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function login(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError("");
    try {
      await api("/auth/login", { method: "POST", body: JSON.stringify({ token }) });
      setToken(""); await onLogin();
    } catch (e: any) { setError(e.message); } finally { setBusy(false); }
  }
  return <div className="loading-screen"><form className="login-card" onSubmit={login}>
    <ShieldCheck size={30} /><h1>登录 Gateway</h1>
    <p>使用管理员访问令牌进入共享工作区。</p>
    <label htmlFor="gateway-token">访问令牌</label>
    <input id="gateway-token" type="password" autoComplete="off" autoFocus required value={token} onChange={e => setToken(e.target.value)} />
    {error && <p role="alert" className="error-box">{error}</p>}
    <button className="button primary" disabled={busy || !token.trim()}>{busy ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}登录</button>
    <p className="muted">令牌不会保存到浏览器存储；登录会话在 8 小时后失效。</p>
  </form></div>;
}
