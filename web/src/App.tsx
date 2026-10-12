import { FormEvent, useEffect, useState } from "react";
import { api, Me, Row } from "./api";
import { Logs, Notifications, Rules, Users } from "./Admin";
import { CatalogPage, resources } from "./Catalog";
import { Dashboard } from "./Dashboard";
import { InvoiceDetail, InvoicesPage } from "./Invoices";
import { go } from "./ui";

function useHash() {
  const [hash, setHash] = useState(window.location.hash.slice(1) || "/");
  useEffect(() => {
    const on = () => setHash(window.location.hash.slice(1) || "/");
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return hash;
}

export function App() {
  const [me, setMe] = useState<Me | null | undefined>(undefined);
  useEffect(() => {
    api<Me>("/auth/me").then(setMe, () => setMe(null));
    const out = () => setMe(null);
    window.addEventListener("pague:logout", out);
    return () => window.removeEventListener("pague:logout", out);
  }, []);
  if (me === undefined) return null;
  if (!me) return <Login onLogin={setMe} />;
  return <Shell me={me} onLogout={() => api("/auth/logout", { method: "POST" }).finally(() => setMe(null))} />;
}

function Login({ onLogin }: { onLogin: (m: Me) => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    try {
      onLogin(await api<Me>("/auth/login", { body: { email, password } }));
    } catch (e: any) {
      setErr(e.message);
    }
  };
  return (
    <main className="login">
      <form onSubmit={submit}>
        <p className="brand">Pague<span>.IA</span></p>
        <h1>Gestão de serviços e faturas</h1>
        <label><span>E-mail</span><input type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} /></label>
        <label><span>Senha</span><input type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} /></label>
        {err && <p className="error" role="alert">{err}</p>}
        <button className="primary">Entrar</button>
      </form>
    </main>
  );
}

const nav: { path: string; label: string; perm?: string; group?: string }[] = [
  { path: "/", label: "Painel" },
  { path: "/faturas", label: "Faturas", perm: "invoices.read" },
  { path: "/expectativas", label: "Expectativas", perm: "services.read" },
  { path: "/servicos", label: "Serviços", perm: "services.read", group: "Cadastros" },
  { path: "/fornecedores", label: "Fornecedores", perm: "suppliers.read" },
  { path: "/contratos", label: "Contratos", perm: "contracts.read" },
  { path: "/unidades", label: "Unidades", perm: "settings.read" },
  { path: "/notificacoes", label: "Notificações", perm: "invoices.read", group: "Operação" },
  { path: "/automacoes", label: "Automações", perm: "settings.read" },
  { path: "/registros", label: "Registros", perm: "logs.read" },
  { path: "/usuarios", label: "Usuários", perm: "users.read" },
];

function Shell({ me, onLogout }: { me: Me; onLogout: () => void }) {
  const hash = useHash();
  const [menu, setMenu] = useState(false);
  const can = (p: string) => me.permissions.includes(p);
  const [, section, id] = hash.split("/");
  useEffect(() => setMenu(false), [hash]);

  let page;
  if (section === "faturas" && id) page = <InvoiceDetail key={id} id={id} can={can} />;
  else if (section === "faturas") page = <InvoicesPage can={can} />;
  else if (resources[section]) page = <CatalogPage key={section} res={resources[section]} can={can} />;
  else if (section === "notificacoes") page = <Notifications can={can} />;
  else if (section === "automacoes") page = <Rules can={can} />;
  else if (section === "registros") page = <Logs />;
  else if (section === "usuarios") page = <Users />;
  else page = <Dashboard can={can} />;

  return (
    <div className="shell">
      <aside className={menu ? "open" : ""}>
        <p className="brand">Pague<span>.IA</span></p>
        <nav>
          {nav.filter((n) => !n.perm || can(n.perm)).map((n) => (
            <div key={n.path}>
              {n.group && <p className="nav-group">{n.group}</p>}
              <a href={"#" + n.path} aria-current={(n.path === "/" ? !section : "/" + section === n.path) ? "page" : undefined}>{n.label}</a>
            </div>
          ))}
        </nav>
        <div className="me">
          <span>{me.user.name}</span>
          <span className="muted small">{me.user.email}</span>
          <button className="ghost small" onClick={onLogout}>Sair</button>
        </div>
      </aside>
      <div className="main">
        <header className="topbar">
          <button className="ghost menu-btn" onClick={() => setMenu(!menu)} aria-label="Menu">Menu</button>
          <Search />
          {me.dry_run && <span className="dry" title="Integrações externas (Whatc, e-mail, webhook) não enviam nada: as mensagens ficam registradas como simuladas.">Integrações em modo simulado</span>}
          {!me.ai && <span className="dry" title="Sem GROQ_API_KEY: a leitura usa regras e linha digitável.">IA desligada</span>}
        </header>
        <main>{page}</main>
      </div>
    </div>
  );
}

const typeLabel: Record<string, string> = { supplier: "Fornecedor", service: "Serviço", contract: "Contrato", invoice: "Fatura" };
const typePath: Record<string, string> = { supplier: "/fornecedores", service: "/servicos", contract: "/contratos" };

function Search() {
  const [q, setQ] = useState("");
  const [rows, setRows] = useState<Row[]>([]);
  useEffect(() => {
    if (q.trim().length < 2) return setRows([]);
    const t = setTimeout(() => api<Row[]>(`/search?q=${encodeURIComponent(q)}`).then(setRows, () => setRows([])), 200);
    return () => clearTimeout(t);
  }, [q]);
  const open = (r: Row) => {
    setQ("");
    go(r.type === "invoice" ? `/faturas/${r.id}` : typePath[r.type]);
  };
  return (
    <div className="search">
      <input type="search" placeholder="Buscar fornecedor, serviço, contrato ou fatura" value={q} onChange={(e) => setQ(e.target.value)} aria-label="Busca global" />
      {rows.length > 0 && (
        <ul className="search-results" role="listbox">
          {rows.map((r) => (
            <li key={r.type + r.id}>
              <button onClick={() => open(r)}>
                <span className="muted small">{typeLabel[r.type]}</span> {r.title} <span className="muted">{r.subtitle}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
