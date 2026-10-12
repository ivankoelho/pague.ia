// Formatação pt-BR e vocabulário de status, compartilhados pela interface.

const brl = new Intl.NumberFormat("pt-BR", { style: "currency", currency: "BRL" });

export function money(v: unknown): string {
  const n = typeof v === "number" ? v : Number(v);
  return v == null || v === "" || Number.isNaN(n) ? "" : brl.format(n);
}

// "2026-11-15" → "15/11/2026"; timestamps ISO viram data e hora local.
export function date(v: unknown): string {
  if (typeof v !== "string" || !v) return "";
  if (/^\d{4}-\d{2}-\d{2}$/.test(v)) return v.split("-").reverse().join("/");
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? v : d.toLocaleString("pt-BR", { dateStyle: "short", timeStyle: "short" });
}

// competência guardada como 1º dia do mês → "10/2026"
export function month(v: unknown): string {
  return typeof v === "string" && v.length >= 7 ? `${v.slice(5, 7)}/${v.slice(0, 4)}` : "";
}

// valor de <input type="month"> → data aceita pela API
export function monthToDate(v: string): string {
  return /^\d{4}-\d{2}$/.test(v) ? `${v}-01` : v;
}

export const invoiceStatus: Record<string, { label: string; tone: Tone }> = {
  RECEIVED: { label: "Recebida", tone: "neutral" },
  PROCESSING: { label: "Processando", tone: "neutral" },
  EXTRACTED: { label: "Extraída", tone: "neutral" },
  IDENTIFIED: { label: "Identificada", tone: "neutral" },
  AUDITING: { label: "Em auditoria", tone: "neutral" },
  REVIEW: { label: "Em revisão", tone: "warn" },
  APPROVED: { label: "Aprovada", tone: "ok" },
  BLOCKED: { label: "Bloqueada", tone: "block" },
  REJECTED: { label: "Rejeitada", tone: "block" },
  FOR_PAYMENT: { label: "No Contas a Pagar", tone: "info" },
  PAID: { label: "Paga", tone: "ok" },
};

export type Tone = "neutral" | "ok" | "warn" | "block" | "info";

export function statusLabel(s: string): string {
  return invoiceStatus[s]?.label ?? s;
}

export function dueText(days: unknown): string {
  if (typeof days !== "number") return "";
  if (days < 0) return `venceu há ${-days} ${-days === 1 ? "dia" : "dias"}`;
  if (days === 0) return "vence hoje";
  return `em ${days} ${days === 1 ? "dia" : "dias"}`;
}
