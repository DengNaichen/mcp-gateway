import React, { useEffect, useState } from "react";
import { Check, Copy, X } from "lucide-react";

export type BadgeTone =
  | "neutral"
  | "success"
  | "warning"
  | "danger"
  | "info"
  | "accent";

export function GatewayLogo({ size = 18 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 32 32"
      fill="none"
      aria-hidden="true"
    >
      <path
        d="M7 7h8v7H7V7Zm10 11h8v7h-8v-7Z"
        stroke="currentColor"
        strokeWidth="2.5"
        strokeLinejoin="round"
      />
      <path
        d="M15 10.5h5.5a4 4 0 0 1 4 4V18M17 21.5h-5.5a4 4 0 0 1-4-4V14"
        stroke="currentColor"
        strokeWidth="2.5"
        strokeLinecap="round"
      />
      <circle cx="24.5" cy="7" r="2.2" fill="currentColor" />
      <circle cx="7.5" cy="25" r="2.2" fill="currentColor" />
    </svg>
  );
}

export function Badge({
  tone = "neutral",
  dot = false,
  children,
}: {
  tone?: BadgeTone;
  dot?: boolean;
  children: React.ReactNode;
}) {
  return (
    <span className={`badge ${tone === "neutral" ? "" : tone}`}>
      {dot && <span className="badge-dot" />}
      {children}
    </span>
  );
}

export function Switch({
  checked,
  label,
  disabled,
  onChange,
}: {
  checked: boolean;
  label: string;
  disabled?: boolean;
  onChange: () => void;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      className={`sail-switch ${checked ? "on" : ""}`}
      onClick={(e) => {
        e.stopPropagation();
        onChange();
      }}
    >
      <span />
    </button>
  );
}

export function CopyButton({
  value,
  label = "复制",
  variant = "keyline",
  onCopied,
}: {
  value: string;
  label?: string;
  variant?: "keyline" | "navy" | "ghost";
  onCopied?: () => void;
}) {
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(false), 1800);
    return () => clearTimeout(timer);
  }, [copied]);

  async function handleCopy(e: React.MouseEvent) {
    e.stopPropagation();
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      onCopied?.();
    } catch {
      // Fallback handled by caller if needed
    }
  }

  const className =
    variant === "navy"
      ? "navy-copy-btn"
      : variant === "ghost"
        ? "btn btn-ghost btn-sm"
        : "btn btn-sm";

  return (
    <button type="button" className={className} onClick={handleCopy}>
      {copied ? <Check size={13} /> : <Copy size={13} />}
      <span>{copied ? "已复制" : label}</span>
    </button>
  );
}

export function AlertBox({
  tone = "error",
  title,
  detail,
  children,
}: {
  tone?: "error" | "warning" | "success" | "info";
  title: string;
  detail?: string;
  children?: React.ReactNode;
}) {
  if (!title) return null;
  return (
    <div
      className={`alert ${tone === "info" ? "" : tone}`}
      role={tone === "error" ? "alert" : "status"}
    >
      <strong>{title}</strong>
      {children}
      {detail && (
        <details style={{ marginTop: 6 }}>
          <summary style={{ cursor: "pointer", fontSize: 12 }}>
            查看诊断详情
          </summary>
          <pre>{detail}</pre>
        </details>
      )}
    </div>
  );
}

export function Modal({
  title,
  onClose,
  footer,
  children,
  maxWidth = 620,
}: {
  title: string;
  onClose: () => void;
  footer?: React.ReactNode;
  children: React.ReactNode;
  maxWidth?: number;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="overlay-backdrop" onClick={onClose}>
      <section
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="modal-dialog"
        style={{ maxWidth }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="modal-header">
          <h2>{title}</h2>
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            aria-label="关闭弹窗"
            onClick={onClose}
          >
            <X size={16} />
          </button>
        </div>
        <div className="modal-body">{children}</div>
        {footer && <div className="modal-footer">{footer}</div>}
      </section>
    </div>
  );
}

export function Drawer({
  title,
  subtitle,
  onClose,
  footer,
  children,
}: {
  title: string;
  subtitle?: string;
  onClose: () => void;
  footer?: React.ReactNode;
  children: React.ReactNode;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="drawer-panel"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="drawer-header">
          <div>
            <h2>{title}</h2>
            {subtitle && (
              <p style={{ fontSize: 12, color: "var(--ink-muted)", marginTop: 2 }}>
                {subtitle}
              </p>
            )}
          </div>
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            aria-label="关闭侧栏"
            onClick={onClose}
          >
            <X size={16} />
          </button>
        </div>
        <div className="drawer-body">{children}</div>
        {footer && <div className="drawer-footer">{footer}</div>}
      </aside>
    </div>
  );
}
