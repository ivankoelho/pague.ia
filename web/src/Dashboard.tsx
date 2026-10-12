import { useState } from "react";
import { Page, Row } from "./api";
import { date, dueText, money, month } from "./format";
import { Badge, Bars, Col, Empty, ErrorLine, go, MonthChart, StatusBadge, Table, useLoad } from "./ui";

type Tab = "executivo" | "vencimentos" | "auditoria";

export function Dashboard({ can }: { can: (p: string) => boolean }) {
  const [tab, setTab] = useState<Tab>("executivo");
  const tabs: [Tab, string][] = [["executivo", "Visão executiva"], ["vencimentos", "Vencimentos"]];
  if (can("audits.read")) tabs.push(["auditoria", "Auditoria"]);
  return (
    <section>
      <header className="page-head">
        <h1>Painel</h1>
        <nav className="tabs" role="tablist">
          {tabs.map(([k, l]) => (
            <button key={k} role="tab" aria-selected={tab === k} className={tab === k ? "on" : ""} onClick={() => setTab(k)}>{l}</button>
          ))}
        </nav>
      </header>
      {tab === "executivo" && <Executive />}
      {tab === "vencimentos" && <Due />}
      {tab === "auditoria" && <AuditView />}
    </section>
  );
}

function thisMonth() {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`;
}

function range(m: string) {
  const [y, mo] = m.split("-").map(Number);
  const last = new Date(y, mo, 0).getDate();
  return { from: `${m}-01`, to: `${m}-${last}` };
}

function Kpi({ label, value, hint, tone }: { label: string; value: string | number; hint?: string; tone?: string }) {
  return (
    <div className={`kpi ${tone ? "kpi-" + tone : ""}`}>
      <span className="kpi-label">{label}</span>
      <span className="kpi-value num">{value}</span>
      {hint && <span className="kpi-hint">{hint}</span>}
    </div>
  );
}

function Executive() {
  const [m, setM] = useState(thisMonth());
  const [supplier, setSupplier] = useState("");
  const [unit, setUnit] = useState("");
  const [category, setCategory] = useState("");
  const p = new URLSearchParams(range(m));
  if (supplier) p.set("supplier_id", supplier);
  if (unit) p.set("unit_id", unit);
  if (category) p.set("category", category);
  const { data, error } = useLoad<Row>(`/dashboard/executive?${p}`);
  const sups = useLoad<Page>("/suppliers?limit=500").data?.items ?? [];
  const units = useLoad<Page>("/units?limit=500").data?.items ?? [];
  const k = data?.kpis ?? {};

  return (
    <>
      <div className="toolbar">
        <label className="inline">Vencimentos em <input type="month" value={m} onChange={(e) => setM(e.target.value || thisMonth())} /></label>
        <select value={supplier} onChange={(e) => setSupplier(e.target.value)} aria-label="Fornecedor">
          <option value="">Todos os fornecedores</option>
          {sups.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
        </select>
        <select value={unit} onChange={(e) => setUnit(e.target.value)} aria-label="Unidade">
          <option value="">Todas as unidades</option>
          {units.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
        </select>
        <input placeholder="Categoria" value={category} onChange={(e) => setCategory(e.target.value)} aria-label="Categoria" />
      </div>
      <ErrorLine msg={error} />
      {data && (
        <>
          <div className="kpis">
            <Kpi label="Despesa recorrente mensal" value={money(k.monthly_recurring)} hint={`${k.active_services} serviços ativos`} />
            <Kpi label="Previsto no período" value={money(k.expected)} />
            <Kpi label="Recebido" value={money(k.received)} hint={`${k.invoices} faturas`} />
            <Kpi label="Aprovado" value={money(k.approved)} />
            <Kpi label="Pago" value={money(k.paid)} />
          </div>
          <div className="kpis kpis-alerts">
            <Kpi label="Pendências" value={k.pending} hint="aguardando processamento ou decisão" />
            <Kpi label="Divergências" value={k.divergences} hint="em revisão ou bloqueadas" tone={k.divergences ? "block" : ""} />
            <Kpi label="Faturas faltantes" value={k.missing} tone={k.missing ? "warn" : ""} />
            <Kpi label="Vencidas sem pagamento" value={k.overdue} tone={k.overdue ? "block" : ""} />
            <Kpi label="Contratos vencendo em 90 dias" value={k.contracts_expiring} hint={`${k.contracts_active} vigentes`} tone={k.contracts_expiring ? "warn" : ""} />
          </div>
          <div className="grid-2">
            <div className="panel">
              <h2>Gasto por competência, últimos 12 meses</h2>
              <MonthChart rows={data.monthly} />
            </div>
            <div className="panel">
              <h2>Por fornecedor</h2>
              <Bars rows={data.by_supplier} />
            </div>
            <div className="panel">
              <h2>Por categoria</h2>
              <Bars rows={data.by_category} />
            </div>
            <div className="panel">
              <h2>Contratos próximos do fim</h2>
              <Table rows={data.contracts_expiring} empty="Nenhum contrato vence nos próximos 90 dias." cols={[
                { key: "number", label: "Contrato" }, { key: "supplier_name", label: "Fornecedor" },
                { key: "ends_on", label: "Término", render: (r) => date(r.ends_on) },
                { key: "days_to_end", label: "Faltam", num: true, render: (r) => `${r.days_to_end} dias` },
              ]} />
            </div>
          </div>
        </>
      )}
    </>
  );
}

const dueCols: Col[] = [
  { key: "due_on", label: "Vencimento", render: (r) => date(r.due_on) },
  { key: "days_to_due", label: "Prazo", render: (r) => <span className={r.days_to_due < 0 ? "text-block" : ""}>{dueText(r.days_to_due)}</span> },
  { key: "supplier_name", label: "Fornecedor" }, { key: "service_name", label: "Serviço" },
  { key: "amount", label: "Valor", num: true, render: (r) => money(r.amount) },
  { key: "status", label: "Status", render: (r) => <StatusBadge status={r.status} /> },
];

function Due() {
  const { data, error } = useLoad<Row>("/dashboard/due");
  const [view, setView] = useState<"lista" | "calendario">("lista");
  if (error) return <ErrorLine msg={error} />;
  if (!data) return null;
  const b = data.buckets;
  return (
    <>
      <div className="kpis">
        <Kpi label="Vencidas" value={b.overdue} hint={money(b.overdue_amount)} tone={b.overdue ? "block" : ""} />
        <Kpi label="Hoje" value={b.today} hint={money(b.today_amount)} tone={b.today ? "warn" : ""} />
        <Kpi label="Até 3 dias" value={b.d3} hint={money(b.d3_amount)} />
        <Kpi label="Até 7 dias" value={b.d7} hint={money(b.d7_amount)} />
        <Kpi label="Até 15 dias" value={b.d15} hint={money(b.d15_amount)} />
        <Kpi label="Até 30 dias" value={b.d30} hint={money(b.d30_amount)} />
      </div>
      <div className="toolbar">
        <div className="seg" role="group" aria-label="Visualização">
          <button className={view === "lista" ? "on" : ""} onClick={() => setView("lista")}>Lista</button>
          <button className={view === "calendario" ? "on" : ""} onClick={() => setView("calendario")}>Calendário</button>
        </div>
      </div>
      {view === "lista" ? (
        <>
          <Table cols={dueCols} rows={data.items} onRow={(r) => go(`/faturas/${r.id}`)} empty="Nenhuma fatura a vencer nos próximos 30 dias." />
          <h2>Esperadas e ainda não recebidas</h2>
          <Table rows={data.expected} empty="Todas as faturas esperadas já chegaram." cols={[
            { key: "due_on", label: "Vencimento esperado", render: (r) => date(r.due_on) },
            { key: "supplier_name", label: "Fornecedor" }, { key: "service_name", label: "Serviço" },
            { key: "amount", label: "Valor esperado", num: true, render: (r) => money(r.amount) },
            { key: "status", label: "Situação", render: (r) => <Badge tone={r.status === "faltante" ? "block" : "neutral"}>{r.status}</Badge> },
          ]} />
        </>
      ) : <Calendar items={data.items} expected={data.expected} />}
    </>
  );
}

function Calendar({ items, expected }: { items: Row[]; expected: Row[] }) {
  const today = new Date();
  const start = new Date(today.getFullYear(), today.getMonth(), 1);
  const [offset, setOffset] = useState(0);
  const first = new Date(start.getFullYear(), start.getMonth() + offset, 1);
  const days = new Date(first.getFullYear(), first.getMonth() + 1, 0).getDate();
  const key = (d: number) => `${first.getFullYear()}-${String(first.getMonth() + 1).padStart(2, "0")}-${String(d).padStart(2, "0")}`;
  const cells = Array.from({ length: first.getDay() }, () => 0).concat(Array.from({ length: days }, (_, i) => i + 1));
  return (
    <div className="calendar">
      <div className="toolbar">
        <button className="ghost" onClick={() => setOffset(offset - 1)} aria-label="Mês anterior">Anterior</button>
        <strong>{first.toLocaleDateString("pt-BR", { month: "long", year: "numeric" })}</strong>
        <button className="ghost" onClick={() => setOffset(offset + 1)} aria-label="Próximo mês">Próximo</button>
      </div>
      <div className="cal-grid">
        {["dom", "seg", "ter", "qua", "qui", "sex", "sáb"].map((d) => <div key={d} className="cal-dow">{d}</div>)}
        {cells.map((d, i) => {
          const inv = d ? items.filter((r) => r.due_on === key(d)) : [];
          const exp = d ? expected.filter((r) => r.due_on === key(d)) : [];
          const isToday = d && first.getFullYear() === today.getFullYear() && first.getMonth() === today.getMonth() && d === today.getDate();
          return (
            <div key={i} className={`cal-day ${d ? "" : "blank"} ${isToday ? "today" : ""}`}>
              {d > 0 && <span className="cal-n">{d}</span>}
              {inv.map((r) => (
                <a key={"i" + r.id} href={`#/faturas/${r.id}`} className={`cal-item tone-${r.status === "BLOCKED" || r.status === "REVIEW" ? "block" : "ok"}`}>
                  {r.supplier_name ?? "#" + r.id} {money(r.amount)}
                </a>
              ))}
              {exp.map((r) => <span key={"e" + r.id} className="cal-item tone-expected">{r.supplier_name} (esperada)</span>)}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function AuditView() {
  const [m, setM] = useState(thisMonth());
  const { data, error } = useLoad<Row>(`/dashboard/audit?${new URLSearchParams(range(m))}`);
  if (error) return <ErrorLine msg={error} />;
  const k = data?.kpis ?? {};
  return (
    <>
      <div className="toolbar">
        <label className="inline">Recebidas em <input type="month" value={m} onChange={(e) => setM(e.target.value || thisMonth())} /></label>
      </div>
      {data && (
        <>
          <div className="kpis">
            <Kpi label="Recebidas" value={k.received} />
            <Kpi label="Aprovadas automaticamente" value={k.auto_approved} />
            <Kpi label="Aprovadas na revisão" value={k.manual_approved} />
            <Kpi label="Em revisão" value={k.review} tone={k.review ? "warn" : ""} />
            <Kpi label="Bloqueadas" value={k.blocked} tone={k.blocked ? "block" : ""} />
            <Kpi label="Rejeitadas" value={k.rejected} />
          </div>
          <div className="kpis kpis-alerts">
            <Kpi label="Duplicidades" value={k.duplicates} tone={k.duplicates ? "block" : ""} />
            <Kpi label="Variações de valor" value={k.amount_variance} tone={k.amount_variance ? "warn" : ""} />
            <Kpi label="Fornecedor desconhecido" value={k.unknown_supplier} />
            <Kpi label="Sem serviço" value={k.without_service} />
            <Kpi label="Sem contrato" value={k.without_contract} />
            <Kpi label="Faltantes" value={k.missing} tone={k.missing ? "warn" : ""} />
          </div>
          <h2>Precisam de decisão</h2>
          <Table rows={data.attention} onRow={(r) => go(`/faturas/${r.id}`)} empty="Nenhuma fatura aguardando decisão." cols={[
            { key: "id", label: "#", num: true }, { key: "supplier_name", label: "Fornecedor" }, { key: "service_name", label: "Serviço" },
            { key: "amount", label: "Valor", num: true, render: (r) => money(r.amount) },
            { key: "due_on", label: "Vencimento", render: (r) => date(r.due_on) },
            { key: "status", label: "Status", render: (r) => <StatusBadge status={r.status} /> },
            { key: "checks", label: "Motivo", render: (r) => ((r.checks ?? []) as Row[]).filter((c) => c.outcome === "FAIL").map((c) => c.label).join(", ") },
          ]} />
          <h2>Faturas faltantes</h2>
          {data.missing.length ? (
            <Table rows={data.missing} cols={[
              { key: "competence", label: "Competência", render: (r) => month(r.competence) },
              { key: "supplier_name", label: "Fornecedor" }, { key: "service_name", label: "Serviço" },
              { key: "expected_due", label: "Vencimento esperado", render: (r) => date(r.expected_due) },
              { key: "expected_amount", label: "Valor esperado", num: true, render: (r) => money(r.expected_amount) },
            ]} />
          ) : <Empty>Nenhuma fatura faltando.</Empty>}
        </>
      )}
    </>
  );
}
