import { FormEvent, useRef, useState } from "react";
import { api, Page, Row } from "./api";
import { fmtDoc } from "./Catalog";
import { date, dueText, invoiceStatus, money, month, monthToDate } from "./format";
import { Badge, Col, Empty, ErrorLine, go, Stamp, StatusBadge, Table, useLoad } from "./ui";

const listCols: Col[] = [
  { key: "id", label: "#", num: true },
  { key: "supplier_name", label: "Fornecedor", render: (r) => r.supplier_name ?? <span className="muted">não identificado</span> },
  { key: "service_name", label: "Serviço" },
  { key: "competence", label: "Competência", render: (r) => month(r.competence) },
  { key: "due_on", label: "Vencimento", render: (r) => <>{date(r.due_on)} {r.status !== "PAID" && r.days_to_due < 0 && <Badge tone="block">vencida</Badge>}</> },
  { key: "amount", label: "Valor", num: true, render: (r) => money(r.amount) },
  { key: "status", label: "Status", render: (r) => <StatusBadge status={r.status} /> },
];

export function InvoicesPage({ can }: { can: (p: string) => boolean }) {
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const file = useRef<HTMLInputElement>(null);
  const p = new URLSearchParams({ limit: "200" });
  if (q) p.set("q", q);
  if (status) p.set("status", status);
  const { data, error, reload } = useLoad<Page>(`/invoices?${p}`);

  const upload = async (files: FileList | null) => {
    if (!files?.length) return;
    setErr(""), setMsg("");
    for (const f of Array.from(files)) {
      const form = new FormData();
      form.append("file", f);
      try {
        const r = await api<Row>("/invoices/upload", { form });
        setMsg(`${f.name}: fatura #${r.invoice_id} recebida e em processamento.` + (r.same_file_invoice_ids ? " Atenção: o mesmo arquivo já foi recebido antes." : ""));
      } catch (e: any) {
        setErr(`${f.name}: ${e.message}`);
      }
    }
    if (file.current) file.current.value = "";
    setTimeout(reload, 1500);
  };

  return (
    <section>
      <header className="page-head">
        <h1>Faturas</h1>
        {can("invoices.write") && (
          <>
            <input ref={file} type="file" accept="application/pdf,.xml,image/png,image/jpeg" multiple hidden onChange={(e) => upload(e.target.files)} />
            <button className="primary" onClick={() => file.current?.click()}>Enviar fatura</button>
          </>
        )}
      </header>
      <div className="toolbar">
        <input type="search" placeholder="Buscar por fornecedor, serviço ou documento" value={q} onChange={(e) => setQ(e.target.value)} aria-label="Buscar faturas" />
        <select value={status} onChange={(e) => setStatus(e.target.value)} aria-label="Status">
          <option value="">Todos os status</option>
          <option value="REVIEW,BLOCKED">Precisam de atenção</option>
          {Object.entries(invoiceStatus).map(([k, v]) => <option key={k} value={k}>{v.label}</option>)}
        </select>
        <button className="ghost" onClick={reload}>Atualizar</button>
      </div>
      {msg && <p className="notice">{msg}</p>}
      <ErrorLine msg={err || error} />
      {data && <Table cols={listCols} rows={data.items} onRow={(r) => go(`/faturas/${r.id}`)} empty="Nenhuma fatura. Envie um PDF ou configure a caixa de e-mail." />}
    </section>
  );
}

const outcomeTone = { OK: "ok", WARN: "warn", FAIL: "block" } as const;

const actions: { to: string; label: string; from: string[]; perm: string; note?: boolean; tone?: string }[] = [
  { to: "AUDITING", label: "Auditar novamente", from: ["RECEIVED", "REVIEW", "BLOCKED"], perm: "audits.execute" },
  { to: "PROCESSING", label: "Reprocessar documento", from: ["REVIEW", "BLOCKED"], perm: "invoices.write" },
  { to: "APPROVED", label: "Aprovar", from: ["REVIEW"], perm: "audits.execute", note: true, tone: "primary" },
  { to: "REVIEW", label: "Enviar para revisão", from: ["BLOCKED", "APPROVED", "FOR_PAYMENT", "REJECTED"], perm: "audits.execute", note: true },
  { to: "REJECTED", label: "Rejeitar", from: ["REVIEW", "BLOCKED", "AUDITING"], perm: "audits.execute", note: true, tone: "danger" },
  { to: "FOR_PAYMENT", label: "Encaminhar ao Contas a Pagar", from: ["APPROVED"], perm: "payments.write", tone: "primary" },
  { to: "PAID", label: "Registrar pagamento", from: ["FOR_PAYMENT"], perm: "payments.write", tone: "primary" },
];

export function InvoiceDetail({ id, can }: { id: string; can: (p: string) => boolean }) {
  const { data, error, reload } = useLoad<Row>(`/invoices/${id}/detail`);
  const [pending, setPending] = useState<(typeof actions)[number] | null>(null);
  const [note, setNote] = useState("");
  const [err, setErr] = useState("");
  const [editing, setEditing] = useState(false);
  if (error) return <ErrorLine msg={error} />;
  if (!data) return <p className="muted">Carregando…</p>;
  const inv = data.invoice as Row;
  const audit = (data.audits as Row[])[0];

  const run = async (a: (typeof actions)[number], n = "") => {
    setErr("");
    try {
      await api(`/invoices/${id}/transition`, { body: { to: a.to, note: n } });
      setPending(null), setNote("");
      setTimeout(reload, a.to === "PROCESSING" ? 1500 : 0);
    } catch (e: any) {
      setErr(e.message);
    }
  };
  const available = actions.filter((a) => a.from.includes(inv.status) && can(a.perm));

  return (
    <section className="detail">
      <a href="#/faturas" className="back">Voltar para faturas</a>
      <header className="detail-head">
        <div>
          <h1>{inv.supplier_name ?? "Fornecedor não identificado"}</h1>
          <p className="muted">
            Fatura #{inv.id}{inv.service_name && `, ${inv.service_name}`}{inv.competence && `, competência ${month(inv.competence)}`}
          </p>
          <dl className="facts">
            <div><dt>Valor</dt><dd className="num big">{money(inv.amount) || "não identificado"}</dd></div>
            <div><dt>Vencimento</dt><dd>{date(inv.due_on) || "não identificado"} <span className="muted">{inv.status !== "PAID" && dueText(inv.days_to_due)}</span></dd></div>
            {inv.tax_id && <div><dt>CNPJ</dt><dd>{fmtDoc(inv.tax_id)}</dd></div>}
            {inv.doc_number && <div><dt>Documento</dt><dd>{inv.doc_number}</dd></div>}
            {inv.unit_name && <div><dt>Unidade</dt><dd>{inv.unit_name}</dd></div>}
            {inv.contract_number && <div><dt>Contrato</dt><dd>{inv.contract_number}</dd></div>}
            {inv.paid_on && <div><dt>Pago em</dt><dd>{date(inv.paid_on)} ({money(inv.paid_amount)})</dd></div>}
          </dl>
        </div>
        <Stamp status={inv.status} />
      </header>

      {(inv.digitable_line || inv.pix) && (
        <div className="payment">
          {inv.digitable_line && <p><span className="muted">Linha digitável</span> <code>{inv.digitable_line}</code></p>}
          {inv.pix && <p><span className="muted">Pix</span> <code className="ellipsis">{inv.pix}</code></p>}
        </div>
      )}

      <div className="actions">
        {available.map((a) => (
          <button key={a.to} className={a.tone ?? ""} onClick={() => (a.note ? setPending(a) : run(a))}>{a.label}</button>
        ))}
        {can("invoices.write") && <button className="ghost" onClick={() => setEditing(!editing)}>{editing ? "Fechar edição" : "Corrigir dados"}</button>}
      </div>
      {pending && (
        <form className="inline-form" onSubmit={(e) => (e.preventDefault(), run(pending, note))}>
          <label>
            <span>Justificativa para "{pending.label.toLowerCase()}"</span>
            <input autoFocus required value={note} onChange={(e) => setNote(e.target.value)} />
          </label>
          <button className="primary">{pending.label}</button>
          <button type="button" className="ghost" onClick={() => setPending(null)}>Cancelar</button>
        </form>
      )}
      <ErrorLine msg={err} />
      {editing && <EditInvoice inv={inv} onSaved={() => (setEditing(false), reload())} />}

      <div className="split">
        <div>
          <h2>Auditoria</h2>
          {audit ? (
            <>
              <p className="muted">Última execução em {date(audit.created_at)}: <StatusBadge status={audit.result} /></p>
              <ul className="checks">
                {(audit.checks as Row[]).map((c) => (
                  <li key={c.code}>
                    <Badge tone={outcomeTone[c.outcome as keyof typeof outcomeTone]}>{c.outcome === "OK" ? "OK" : c.outcome === "WARN" ? "Atenção" : "Falhou"}</Badge>
                    <strong>{c.label}</strong>
                    <span>{c.detail}</span>
                    {c.evidence && <span className="evidence">{Object.entries(c.evidence).map(([k, v]) => `${k.replace("_", " ")}: ${Array.isArray(v) ? v.join(", ") : v}`).join("; ")}</span>}
                  </li>
                ))}
              </ul>
            </>
          ) : <Empty>Ainda não auditada.</Empty>}

          <h2>Documentos</h2>
          {(data.documents as Row[]).length ? (
            <ul className="plain">
              {(data.documents as Row[]).map((d) => (
                <li key={d.id}>
                  <a href={`/api/documents/${d.id}`} target="_blank" rel="noopener">{d.filename}</a>{" "}
                  <span className="muted">{(d.size / 1024).toFixed(0)} KB, via {d.source}, {date(d.created_at)}</span>
                </li>
              ))}
            </ul>
          ) : <Empty>Sem documento anexado.</Empty>}

          <h2>Leitura por IA</h2>
          {(data.ai_runs as Row[]).length ? (
            <ul className="plain">
              {(data.ai_runs as Row[]).map((r) => (
                <li key={r.id}>
                  <Badge tone={r.ok ? "ok" : "block"}>{r.ok ? "ok" : "falhou"}</Badge> {r.provider} / {r.model}, prompt {r.prompt_version}, {r.duration_ms} ms
                  {r.error && <div className="error">{r.error}</div>}
                  {r.output && <details><summary>Resposta</summary><pre>{r.output}</pre></details>}
                </li>
              ))}
            </ul>
          ) : <Empty>Extração feita só por regras (IA não configurada ou sem documento).</Empty>}
        </div>

        <div>
          <h2>Histórico</h2>
          <ol className="timeline">
            {(data.events as Row[]).map((e) => (
              <li key={e.id}>
                <span className="muted">{date(e.at)}</span>
                <span>{e.from_status ? `${invoiceStatus[e.from_status]?.label ?? e.from_status} para ` : ""}<strong>{invoiceStatus[e.to_status]?.label ?? e.to_status}</strong></span>
                <span className="muted">por {e.actor}</span>
                {e.note && <span className="note">{e.note}</span>}
              </li>
            ))}
          </ol>

          <h2>Comunicações</h2>
          {(data.notifications as Row[]).length ? (
            <ul className="plain">
              {(data.notifications as Row[]).map((n) => (
                <li key={n.id}>
                  <Badge tone={n.status === "sent" ? "ok" : n.status === "failed" ? "block" : n.status === "mock" ? "info" : "neutral"}>{notifLabel[n.status] ?? n.status}</Badge>{" "}
                  {channelName[n.channel] ?? n.channel}{n.target && ` para ${n.target}`}: {n.subject}
                  <details><summary>Mensagem</summary><pre>{n.body}</pre></details>
                </li>
              ))}
            </ul>
          ) : <Empty>Nenhuma comunicação enviada.</Empty>}
        </div>
      </div>
    </section>
  );
}

export const notifLabel: Record<string, string> = { sent: "enviada", failed: "falhou", mock: "simulada", pending: "na fila" };

export const channelName: Record<string, string> = { in_app: "Aviso interno", email: "E-mail", webhook: "Webhook", whatc: "Whatc (WhatsApp)" };

function EditInvoice({ inv, onSaved }: { inv: Row; onSaved: () => void }) {
  const fields: [string, string, string][] = [
    ["amount", "Valor (R$)", "number"], ["due_on", "Vencimento", "date"], ["competence", "Competência", "month"],
    ["issued_on", "Emissão", "date"], ["doc_number", "Nº do documento", "text"], ["tax_id", "CNPJ", "text"],
    ["digitable_line", "Linha digitável", "text"], ["pix", "Pix", "text"], ["notes", "Observações", "text"],
  ];
  const [v, setV] = useState<Row>({ ...inv, competence: inv.competence?.slice(0, 7) });
  const [reason, setReason] = useState("");
  const [err, setErr] = useState("");
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const body: Row = { _reason: reason };
    for (const [k] of fields) {
      const val = k === "competence" && v[k] ? monthToDate(v[k]) : v[k];
      if (String(val ?? "") !== String(inv[k] ?? "")) body[k] = val === "" ? null : val;
    }
    try {
      await api(`/invoices/${inv.id}`, { method: "PATCH", body });
      onSaved();
    } catch (e: any) {
      setErr(e.message);
    }
  };
  return (
    <form className="panel" onSubmit={submit}>
      <div className="form-grid">
        {fields.map(([k, l, t]) => (
          <label key={k} className={k === "digitable_line" || k === "pix" || k === "notes" ? "wide" : ""}>
            <span>{l}</span>
            <input type={t} step="any" value={v[k] ?? ""} onChange={(e) => setV({ ...v, [k]: t === "number" && e.target.value ? Number(e.target.value) : e.target.value })} />
          </label>
        ))}
        <label className="wide"><span>Motivo da correção</span><input required value={reason} onChange={(e) => setReason(e.target.value)} /></label>
      </div>
      <ErrorLine msg={err} />
      <div className="actions">
        <button className="primary">Salvar correção</button>
        <span className="muted">Depois de salvar, use "Auditar novamente" para validar com os dados corrigidos.</span>
      </div>
    </form>
  );
}
