import { ReactNode, useCallback, useEffect, useState } from "react";
import { api, Row } from "./api";
import { invoiceStatus, money, Tone } from "./format";

export function useLoad<T>(path: string | null, deps: unknown[] = []) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState("");
  const reload = useCallback(() => {
    if (!path) return;
    api<T>(path).then(
      (d) => (setData(d), setError("")),
      (e) => setError(e.message),
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, ...deps]);
  useEffect(reload, [reload]);
  return { data, error, reload };
}

export function Badge({ tone = "neutral", children }: { tone?: Tone; children: ReactNode }) {
  return <span className={`badge badge-${tone}`}>{children}</span>;
}

export function StatusBadge({ status }: { status: string }) {
  const s = invoiceStatus[status];
  return <Badge tone={s?.tone}>{s?.label ?? status}</Badge>;
}

// O carimbo: status da fatura como se estivesse carimbado no papel.
export function Stamp({ status }: { status: string }) {
  const s = invoiceStatus[status];
  return (
    <div className={`stamp stamp-${s?.tone ?? "neutral"}`} role="status">
      {s?.label ?? status}
    </div>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="empty">{children}</p>;
}

export function ErrorLine({ msg }: { msg: string }) {
  return msg ? <p className="error" role="alert">{msg}</p> : null;
}

// Barras horizontais: rótulo, barra proporcional, valor.
export function Bars({ rows, format = money }: { rows: Row[]; format?: (v: number) => string }) {
  const max = Math.max(1, ...rows.map((r) => Number(r.value) || 0));
  if (!rows.length) return <Empty>Sem dados no período.</Empty>;
  return (
    <ul className="bars">
      {rows.map((r) => (
        <li key={r.label}>
          <span className="bars-label">{r.label}</span>
          <span className="bars-track">
            <span className="bars-fill" style={{ width: `${((Number(r.value) || 0) / max) * 100}%` }} />
          </span>
          <span className="num">{format(Number(r.value))}</span>
        </li>
      ))}
    </ul>
  );
}

// Colunas mensais: realizado (barra) x esperado (traço).
export function MonthChart({ rows }: { rows: Row[] }) {
  const max = Math.max(1, ...rows.flatMap((r) => [Number(r.actual), Number(r.expected)]));
  const W = 720, H = 180, bw = W / rows.length;
  return (
    <figure className="chart">
      <svg viewBox={`0 0 ${W} ${H + 22}`} role="img" aria-label="Gasto realizado e esperado por competência">
        {rows.map((r, i) => {
          const h = (Number(r.actual) / max) * H;
          const e = H - (Number(r.expected) / max) * H;
          return (
            <g key={r.label}>
              <title>{`${r.label}: realizado ${money(r.actual)}, esperado ${money(r.expected)}`}</title>
              <rect x={i * bw + bw * 0.2} y={H - h} width={bw * 0.6} height={h} className="chart-bar" />
              {Number(r.expected) > 0 && <line x1={i * bw + bw * 0.12} x2={i * bw + bw * 0.88} y1={e} y2={e} className="chart-expected" />}
              <text x={i * bw + bw / 2} y={H + 16} textAnchor="middle" className="chart-label">{r.label.slice(0, 2)}/{r.label.slice(5)}</text>
            </g>
          );
        })}
      </svg>
      <figcaption>
        <span className="key key-bar" /> Realizado <span className="key key-line" /> Esperado
      </figcaption>
    </figure>
  );
}

export type Col = { key: string; label: string; render?: (r: Row) => ReactNode; num?: boolean };

export function Table({ cols, rows, onRow, empty = "Nada por aqui ainda." }: { cols: Col[]; rows: Row[]; onRow?: (r: Row) => void; empty?: string }) {
  if (!rows.length) return <Empty>{empty}</Empty>;
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>{cols.map((c) => <th key={c.key} className={c.num ? "num" : ""}>{c.label}</th>)}</tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={r.id ?? i} onClick={onRow ? () => onRow(r) : undefined} className={onRow ? "clickable" : ""}
              tabIndex={onRow ? 0 : undefined} onKeyDown={onRow ? (e) => e.key === "Enter" && onRow(r) : undefined}>
              {cols.map((c) => <td key={c.key} className={c.num ? "num" : ""}>{c.render ? c.render(r) : String(r[c.key] ?? "")}</td>)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function go(path: string) {
  window.location.hash = path;
}
