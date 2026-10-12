import { FormEvent, useState } from "react";
import { api, Row } from "./api";
import { date } from "./format";
import { channelName, notifLabel } from "./Invoices";
import { Badge, ErrorLine, Table, useLoad } from "./ui";

const notifTone = (s: string) => (s === "sent" ? "ok" : s === "failed" ? "block" : s === "mock" ? "info" : "neutral");

export function Notifications({ can }: { can: (p: string) => boolean }) {
  const [channel, setChannel] = useState("");
  const { data, error, reload } = useLoad<Row[]>(`/notifications${channel ? "?channel=" + channel : ""}`);
  const retry = async (id: number) => (await api(`/notifications/${id}/retry`, { method: "POST" }), reload());
  return (
    <section>
      <header className="page-head"><h1>Notificações</h1></header>
      <div className="toolbar">
        <select value={channel} onChange={(e) => setChannel(e.target.value)} aria-label="Canal">
          <option value="">Todos os canais</option>
          {Object.entries(channelName).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
      </div>
      <ErrorLine msg={error} />
      {data && <Table rows={data} empty="Nenhuma notificação." cols={[
        { key: "created_at", label: "Quando", render: (r) => date(r.created_at) },
        { key: "channel", label: "Canal", render: (r) => channelName[r.channel] ?? r.channel },
        { key: "subject", label: "Assunto", render: (r) => <details><summary>{r.subject}</summary><pre>{r.body}</pre></details> },
        { key: "ref_id", label: "Referência", render: (r) => r.ref_entity === "invoice" ? <a href={`#/faturas/${r.ref_id}`}>Fatura #{r.ref_id}</a> : r.ref_entity ? `${r.ref_entity} #${r.ref_id}` : "" },
        { key: "status", label: "Situação", render: (r) => <><Badge tone={notifTone(r.status)}>{notifLabel[r.status] ?? r.status}</Badge>{r.last_error && r.status !== "mock" && <div className="error small">{r.last_error}</div>}</> },
        { key: "retry", label: "", render: (r) => can("settings.write") && (r.status === "failed" || r.status === "mock") ? <button className="ghost small" onClick={() => retry(r.id)}>Reenviar</button> : null },
      ]} />}
    </section>
  );
}

const eventName: Record<string, string> = {
  "invoice.status_changed": "Fatura mudou de status", "invoice.due_soon": "Vencimento próximo",
  "contract.expiring": "Contrato vencendo", "expectation.missing": "Fatura não recebida",
};

export function Rules({ can }: { can: (p: string) => boolean }) {
  const { data, error, reload } = useLoad<Row[]>("/automation/rules");
  const [msg, setMsg] = useState("");
  const toggle = async (r: Row) => (await api(`/automation/rules/${r.id}`, { body: { enabled: !r.enabled } }), reload());
  const scan = async () => {
    await api("/automation/scan", { method: "POST" });
    setMsg("Varredura concluída: expectativas, faltantes, vencimentos e contratos verificados.");
  };
  return (
    <section>
      <header className="page-head">
        <h1>Automações</h1>
        {can("settings.write") && <button onClick={scan}>Executar varredura agora</button>}
      </header>
      <p className="muted">Quando o evento acontece e as condições batem, a ação é executada. A varredura roda a cada hora.</p>
      {msg && <p className="notice">{msg}</p>}
      <ErrorLine msg={error} />
      {data && <Table rows={data} cols={[
        { key: "name", label: "Regra" },
        { key: "event", label: "Quando", render: (r) => eventName[r.event] ?? r.event },
        { key: "conditions", label: "Condições", render: (r) => ((r.conditions ?? []) as Row[]).map((c) => `${c.field} ${c.op} ${JSON.stringify(c.value)}`).join(" e ") || "sempre" },
        { key: "action", label: "Então", render: (r) => r.action === "notify" ? `Notificar (${channelName[r.params?.channel] ?? r.params?.channel}${r.params?.target ? " " + r.params.target : ""})` : `Mover para ${r.params?.to}` },
        { key: "runs", label: "Execuções", num: true, render: (r) => <>{r.runs}{r.last_run && <div className="muted small">{date(r.last_run)}</div>}</> },
        { key: "enabled", label: "Ativa", render: (r) => can("settings.write")
          ? <label className="switch"><input type="checkbox" checked={r.enabled} onChange={() => toggle(r)} /> {r.enabled ? "sim" : "não"}</label>
          : r.enabled ? "sim" : "não" },
      ]} />}
    </section>
  );
}

export function Logs() {
  const [tab, setTab] = useState<"audit" | "jobs">("audit");
  const audit = useLoad<Row[]>(tab === "audit" ? "/logs/audit" : null);
  const jobs = useLoad<Row[]>(tab === "jobs" ? "/logs/jobs" : null);
  return (
    <section>
      <header className="page-head">
        <h1>Registros</h1>
        <nav className="tabs">
          <button className={tab === "audit" ? "on" : ""} onClick={() => setTab("audit")}>Trilha de auditoria</button>
          <button className={tab === "jobs" ? "on" : ""} onClick={() => setTab("jobs")}>Tarefas em segundo plano</button>
        </nav>
      </header>
      {tab === "audit" && audit.data && <Table rows={audit.data} cols={[
        { key: "at", label: "Quando", render: (r) => date(r.at) },
        { key: "user_name", label: "Quem" }, { key: "action", label: "Ação" },
        { key: "entity", label: "Registro", render: (r) => r.entity === "invoice" ? <a href={`#/faturas/${r.entity_id}`}>fatura #{r.entity_id}</a> : `${r.entity} #${r.entity_id ?? ""}` },
        { key: "after", label: "Mudança", render: (r) => <Change before={r.before} after={r.after} /> },
        { key: "reason", label: "Motivo" }, { key: "ip", label: "IP" },
      ]} />}
      {tab === "jobs" && jobs.data && <Table rows={jobs.data} cols={[
        { key: "id", label: "#", num: true }, { key: "kind", label: "Tarefa" },
        { key: "status", label: "Situação", render: (r) => <Badge tone={r.status === "done" ? "ok" : r.status === "failed" ? "block" : "neutral"}>{r.status}</Badge> },
        { key: "attempts", label: "Tentativas", num: true, render: (r) => `${r.attempts}/${r.max_attempts}` },
        { key: "duration_ms", label: "Duração", num: true, render: (r) => r.duration_ms != null ? `${r.duration_ms} ms` : "" },
        { key: "created_at", label: "Criada", render: (r) => date(r.created_at) },
        { key: "last_error", label: "Erro", render: (r) => <span className="error small">{r.last_error}</span> },
      ]} />}
      <ErrorLine msg={audit.error || jobs.error} />
    </section>
  );
}

function Change({ before, after }: { before: Row | null; after: Row | null }) {
  if (!after) return null;
  return (
    <ul className="plain small">
      {Object.entries(after).map(([k, v]) => (
        <li key={k}><span className="muted">{k}:</span> {before && k in before ? <>{String(before[k] ?? "vazio")} para </> : null}{String(v ?? "vazio")}</li>
      ))}
    </ul>
  );
}

const roles: [string, string][] = [["admin", "Administrador"], ["gestor", "Gestor"], ["auditor", "Auditor"], ["ti", "TI"], ["financeiro", "Financeiro"], ["consulta", "Consulta"]];

export function Users() {
  const { data, error, reload } = useLoad<Row[]>("/users");
  const [form, setForm] = useState<Row | null>(null);
  const [err, setErr] = useState("");
  const save = async (e: FormEvent) => {
    e.preventDefault();
    try {
      await api(form!.id ? `/users/${form!.id}` : "/users", { body: form });
      setForm(null), reload();
    } catch (e: any) {
      setErr(e.message);
    }
  };
  return (
    <section>
      <header className="page-head">
        <h1>Usuários</h1>
        <button className="primary" onClick={() => (setErr(""), setForm({ role: "consulta" }))}>Novo usuário</button>
      </header>
      <ErrorLine msg={error} />
      {form && (
        <form className="panel" onSubmit={save}>
          <div className="form-grid">
            <label><span>Nome *</span><input required value={form.name ?? ""} onChange={(e) => setForm({ ...form, name: e.target.value })} /></label>
            <label><span>E-mail *</span><input type="email" required disabled={!!form.id} value={form.email ?? ""} onChange={(e) => setForm({ ...form, email: e.target.value })} /></label>
            <label><span>Papel</span>
              <select value={form.role} onChange={(e) => setForm({ ...form, role: e.target.value })}>{roles.map(([k, l]) => <option key={k} value={k}>{l}</option>)}</select>
            </label>
            <label><span>{form.id ? "Nova senha (opcional)" : "Senha *"}</span>
              <input type="password" minLength={10} required={!form.id} value={form.password ?? ""} onChange={(e) => setForm({ ...form, password: e.target.value })} autoComplete="new-password" />
            </label>
            {form.id && <label><span>Ativo</span><input type="checkbox" checked={form.active} onChange={(e) => setForm({ ...form, active: e.target.checked })} /></label>}
          </div>
          <ErrorLine msg={err} />
          <div className="actions">
            <button className="primary">{form.id ? "Salvar usuário" : "Criar usuário"}</button>
            <button type="button" className="ghost" onClick={() => setForm(null)}>Cancelar</button>
          </div>
        </form>
      )}
      {data && <Table rows={data} onRow={(r) => (setErr(""), setForm({ ...r, password: "" }))} cols={[
        { key: "name", label: "Nome" }, { key: "email", label: "E-mail" },
        { key: "role", label: "Papel", render: (r) => roles.find(([k]) => k === r.role)?.[1] ?? r.role },
        { key: "active", label: "Situação", render: (r) => <Badge tone={r.active ? "ok" : "neutral"}>{r.active ? "ativo" : "inativo"}</Badge> },
      ]} />}
    </section>
  );
}
