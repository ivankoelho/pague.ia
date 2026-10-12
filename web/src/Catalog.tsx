import { FormEvent, useEffect, useState } from "react";
import { api, Page, Row } from "./api";
import { date, money, month, monthToDate } from "./format";
import { Badge, Col, ErrorLine, Table, useLoad } from "./ui";

// Cadastros declarativos: mesma tela para fornecedores, serviços, contratos, expectativas e unidades.

type Field = {
  name: string;
  label: string;
  type?: "text" | "number" | "date" | "month" | "bool" | "textarea" | "select" | "ref";
  options?: [string, string][];
  ref?: string; // recurso referenciado (lista de opções)
  required?: boolean;
  wide?: boolean;
};

export type Resource = {
  path: string;
  title: string;
  singular: string;
  perm: string;
  cols: Col[];
  fields: Field[];
  filters?: { name: string; label: string; options: [string, string][] }[];
};

const statusTone: Record<string, "ok" | "warn" | "block" | "neutral"> = {
  ativo: "ok", vigente: "ok", recebida: "ok", pendente: "neutral", em_implantacao: "neutral", em_negociacao: "warn",
  suspenso: "warn", faltante: "block", inativo: "neutral", cancelado: "block", encerrado: "neutral", dispensada: "neutral",
};
const st = (r: Row) => <Badge tone={statusTone[r.status] ?? "neutral"}>{String(r.status ?? "").replace("_", " ")}</Badge>;
const periodicity: [string, string][] = [["mensal", "Mensal"], ["bimestral", "Bimestral"], ["trimestral", "Trimestral"], ["semestral", "Semestral"], ["anual", "Anual"]];

export const resources: Record<string, Resource> = {
  fornecedores: {
    path: "suppliers", title: "Fornecedores", singular: "fornecedor", perm: "suppliers",
    cols: [
      { key: "name", label: "Nome" }, { key: "legal_name", label: "Razão social" },
      { key: "tax_id", label: "CNPJ/CPF", render: (r) => fmtDoc(r.tax_id) }, { key: "email", label: "E-mail" },
      { key: "status", label: "Status", render: st },
    ],
    fields: [
      { name: "name", label: "Nome", required: true }, { name: "legal_name", label: "Razão social" },
      { name: "trade_name", label: "Nome fantasia" }, { name: "tax_id", label: "CNPJ/CPF" },
      { name: "email", label: "E-mail" }, { name: "phone", label: "Telefone" },
      { name: "contacts", label: "Contatos", wide: true }, { name: "address", label: "Endereço", wide: true },
      { name: "status", label: "Status", type: "select", options: [["ativo", "Ativo"], ["inativo", "Inativo"]] },
      { name: "notes", label: "Observações", type: "textarea", wide: true },
    ],
    filters: [{ name: "status", label: "Status", options: [["ativo", "Ativos"], ["inativo", "Inativos"]] }],
  },
  servicos: {
    path: "services", title: "Serviços", singular: "serviço", perm: "services",
    cols: [
      { key: "name", label: "Serviço" }, { key: "supplier_name", label: "Fornecedor" }, { key: "category", label: "Categoria" },
      { key: "unit_name", label: "Unidade" }, { key: "owner", label: "Responsável" },
      { key: "expected_amount", label: "Valor esperado", num: true, render: (r) => money(r.expected_amount) },
      { key: "due_day", label: "Vence dia", num: true }, { key: "status", label: "Status", render: st },
    ],
    fields: [
      { name: "name", label: "Nome", required: true }, { name: "supplier_id", label: "Fornecedor", type: "ref", ref: "suppliers", required: true },
      { name: "category", label: "Categoria" }, { name: "contract_id", label: "Contrato", type: "ref", ref: "contracts" },
      { name: "unit_id", label: "Unidade", type: "ref", ref: "units" }, { name: "owner", label: "Responsável" },
      { name: "cost_center", label: "Centro de custo" }, { name: "periodicity", label: "Periodicidade", type: "select", options: periodicity },
      { name: "expected_amount", label: "Valor esperado (R$)", type: "number" }, { name: "tolerance_pct", label: "Tolerância (%)", type: "number" },
      { name: "due_day", label: "Dia de vencimento", type: "number" },
      { name: "status", label: "Status", type: "select", options: [["ativo", "Ativo"], ["em_implantacao", "Em implantação"], ["suspenso", "Suspenso"], ["cancelado", "Cancelado"], ["encerrado", "Encerrado"]] },
      { name: "criticality", label: "Criticidade", type: "select", options: [["baixa", "Baixa"], ["media", "Média"], ["alta", "Alta"], ["critica", "Crítica"]] },
      { name: "starts_on", label: "Início", type: "date" }, { name: "ends_on", label: "Término", type: "date" },
      { name: "renewal_on", label: "Renovação", type: "date" },
      { name: "description", label: "Descrição", type: "textarea", wide: true }, { name: "notes", label: "Observações", type: "textarea", wide: true },
    ],
    filters: [{ name: "status", label: "Status", options: [["ativo", "Ativos"], ["suspenso", "Suspensos"], ["cancelado", "Cancelados"], ["encerrado", "Encerrados"]] }],
  },
  contratos: {
    path: "contracts", title: "Contratos", singular: "contrato", perm: "contracts",
    cols: [
      { key: "number", label: "Número" }, { key: "supplier_name", label: "Fornecedor" },
      { key: "starts_on", label: "Início", render: (r) => date(r.starts_on) },
      { key: "ends_on", label: "Término", render: (r) => <>{date(r.ends_on)} {endsBadge(r)}</> },
      { key: "amount", label: "Valor", num: true, render: (r) => money(r.amount) },
      { key: "adjustment_index", label: "Reajuste", render: (r) => [r.adjustment_index, r.adjustment_pct && `${r.adjustment_pct}%`].filter(Boolean).join(" ") },
      { key: "status", label: "Status", render: st },
    ],
    fields: [
      { name: "number", label: "Número", required: true }, { name: "supplier_id", label: "Fornecedor", type: "ref", ref: "suppliers", required: true },
      { name: "starts_on", label: "Início", type: "date", required: true }, { name: "ends_on", label: "Término", type: "date" },
      { name: "auto_renew", label: "Renova automaticamente", type: "bool" }, { name: "periodicity", label: "Periodicidade", type: "select", options: periodicity },
      { name: "amount", label: "Valor contratado (R$)", type: "number" }, { name: "adjustment_index", label: "Índice de reajuste" },
      { name: "adjustment_pct", label: "Percentual (%)", type: "number" }, { name: "owner", label: "Responsável" },
      { name: "status", label: "Status", type: "select", options: [["vigente", "Vigente"], ["em_negociacao", "Em negociação"], ["encerrado", "Encerrado"], ["cancelado", "Cancelado"]] },
      { name: "notes", label: "Observações", type: "textarea", wide: true },
    ],
    filters: [{ name: "status", label: "Status", options: [["vigente", "Vigentes"], ["encerrado", "Encerrados"], ["cancelado", "Cancelados"]] }],
  },
  expectativas: {
    path: "expectations", title: "Expectativas de cobrança", singular: "expectativa", perm: "services",
    cols: [
      { key: "competence", label: "Competência", render: (r) => month(r.competence) }, { key: "service_name", label: "Serviço" },
      { key: "supplier_name", label: "Fornecedor" }, { key: "expected_amount", label: "Valor esperado", num: true, render: (r) => money(r.expected_amount) },
      { key: "expected_due", label: "Vencimento esperado", render: (r) => date(r.expected_due) },
      { key: "invoice_id", label: "Fatura", render: (r) => (r.invoice_id ? <a href={`#/faturas/${r.invoice_id}`} onClick={(e) => e.stopPropagation()}>#{r.invoice_id}</a> : "") },
      { key: "status", label: "Situação", render: st },
    ],
    fields: [
      { name: "service_id", label: "Serviço", type: "ref", ref: "services", required: true }, { name: "competence", label: "Competência", type: "month", required: true },
      { name: "expected_amount", label: "Valor esperado (R$)", type: "number" }, { name: "expected_due", label: "Vencimento esperado", type: "date" },
      { name: "status", label: "Situação", type: "select", options: [["pendente", "Pendente"], ["recebida", "Recebida"], ["faltante", "Faltante"], ["dispensada", "Dispensada"]] },
      { name: "notes", label: "Observações", type: "textarea", wide: true },
    ],
    filters: [{ name: "status", label: "Situação", options: [["pendente", "Pendentes"], ["faltante", "Faltantes"], ["recebida", "Recebidas"]] }],
  },
  unidades: {
    path: "units", title: "Unidades", singular: "unidade", perm: "settings",
    cols: [{ key: "name", label: "Nome" }, { key: "kind", label: "Tipo" }],
    fields: [
      { name: "name", label: "Nome", required: true },
      { name: "kind", label: "Tipo", type: "select", options: [["empresa", "Empresa"], ["unidade", "Unidade"], ["departamento", "Departamento"]] },
      { name: "parent_id", label: "Pertence a", type: "ref", ref: "units" },
    ],
  },
};

function endsBadge(r: Row) {
  const d = r.days_to_end;
  if (r.status !== "vigente" || typeof d !== "number") return null;
  if (d < 0) return <Badge tone="block">expirado</Badge>;
  if (d <= 90) return <Badge tone="warn">{d} dias</Badge>;
  return null;
}

export function fmtDoc(v: unknown) {
  const s = String(v ?? "");
  if (s.length === 14) return s.replace(/(\d{2})(\d{3})(\d{3})(\d{4})(\d{2})/, "$1.$2.$3/$4-$5");
  if (s.length === 11) return s.replace(/(\d{3})(\d{3})(\d{3})(\d{2})/, "$1.$2.$3-$4");
  return s;
}

const refLabel = (r: Row) => r.number ? `${r.number} (${r.supplier_name})` : r.supplier_name && r.name ? `${r.name} (${r.supplier_name})` : r.name;

export function CatalogPage({ res, can }: { res: Resource; can: (p: string) => boolean }) {
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState("");
  const [editing, setEditing] = useState<Row | null>(null);
  const params = new URLSearchParams({ limit: "200" });
  if (q) params.set("q", q);
  if (filter && res.filters) params.set(res.filters[0].name, filter);
  const { data, error, reload } = useLoad<Page>(`/${res.path}?${params}`);
  const writable = can(res.perm + ".write");

  return (
    <section>
      <header className="page-head">
        <h1>{res.title}</h1>
        {writable && <button className="primary" onClick={() => setEditing({})}>Novo {res.singular}</button>}
      </header>
      <div className="toolbar">
        <input type="search" placeholder={`Buscar ${res.title.toLowerCase()}`} value={q} onChange={(e) => setQ(e.target.value)} aria-label="Buscar" />
        {res.filters?.map((f) => (
          <select key={f.name} value={filter} onChange={(e) => setFilter(e.target.value)} aria-label={f.label}>
            <option value="">Todos</option>
            {f.options.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
          </select>
        ))}
        {data && <span className="muted">{data.total} {data.total === 1 ? "registro" : "registros"}</span>}
      </div>
      <ErrorLine msg={error} />
      {data && <Table cols={res.cols} rows={data.items} onRow={writable ? setEditing : undefined} empty={`Nenhum ${res.singular} cadastrado.`} />}
      {editing && <EditForm res={res} row={editing} onClose={(saved) => (setEditing(null), saved && reload())} />}
    </section>
  );
}

function EditForm({ res, row, onClose }: { res: Resource; row: Row; onClose: (saved: boolean) => void }) {
  const [vals, setVals] = useState<Row>(() => ({ ...row, competence: row.competence?.slice(0, 7) }));
  const [reason, setReason] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const isNew = !row.id;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    const body: Row = {};
    for (const f of res.fields) {
      let v = vals[f.name];
      if (v === "" || v === undefined) v = null;
      if (f.type === "month" && v) v = monthToDate(v);
      if (isNew ? v !== null : String(v ?? "") !== String(row[f.name] ?? "")) body[f.name] = v;
    }
    if (!isNew) body._reason = reason;
    try {
      await api(`/${res.path}${isNew ? "" : "/" + row.id}`, { method: isNew ? "POST" : "PATCH", body });
      onClose(true);
    } catch (e: any) {
      setErr(e.message);
      setBusy(false);
    }
  };

  return (
    <div className="drawer-backdrop" onClick={() => onClose(false)}>
      <form className="drawer" onSubmit={submit} onClick={(e) => e.stopPropagation()} aria-label={`Editar ${res.singular}`}>
        <header className="page-head">
          <h2>{isNew ? `Novo ${res.singular}` : `Editar ${res.singular}`}</h2>
          <button type="button" className="ghost" onClick={() => onClose(false)} aria-label="Fechar">Fechar</button>
        </header>
        <div className="form-grid">
          {res.fields.map((f) => (
            <label key={f.name} className={f.wide || f.type === "textarea" ? "wide" : ""}>
              <span>{f.label}{f.required && " *"}</span>
              <Input f={f} value={vals[f.name]} onChange={(v) => setVals({ ...vals, [f.name]: v })} />
            </label>
          ))}
          {!isNew && (
            <label className="wide">
              <span>Motivo da alteração</span>
              <input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Ex.: reajuste contratual" />
            </label>
          )}
        </div>
        <ErrorLine msg={err} />
        <footer className="actions">
          <button className="primary" disabled={busy}>{isNew ? `Criar ${res.singular}` : "Salvar alterações"}</button>
        </footer>
      </form>
    </div>
  );
}

function Input({ f, value, onChange }: { f: Field; value: any; onChange: (v: any) => void }) {
  const [opts, setOpts] = useState<Row[]>([]);
  useEffect(() => {
    if (f.ref) api<Page>(`/${f.ref}?limit=500`).then((p) => setOpts(p.items), () => {});
  }, [f.ref]);
  const v = value ?? "";
  switch (f.type) {
    case "textarea":
      return <textarea rows={3} value={v} onChange={(e) => onChange(e.target.value)} />;
    case "bool":
      return <input type="checkbox" checked={!!value} onChange={(e) => onChange(e.target.checked)} />;
    case "select":
      return (
        <select value={v} onChange={(e) => onChange(e.target.value)} required={f.required}>
          <option value="">Selecione</option>
          {f.options!.map(([k, l]) => <option key={k} value={k}>{l}</option>)}
        </select>
      );
    case "ref":
      return (
        <select value={v} onChange={(e) => onChange(e.target.value ? Number(e.target.value) : null)} required={f.required}>
          <option value="">Selecione</option>
          {opts.map((o) => <option key={o.id} value={o.id}>{refLabel(o)}</option>)}
        </select>
      );
    case "number":
      return <input type="number" step="any" value={v} onChange={(e) => onChange(e.target.value === "" ? null : Number(e.target.value))} required={f.required} />;
    default:
      return <input type={f.type ?? "text"} value={v} onChange={(e) => onChange(e.target.value)} required={f.required} />;
  }
}
