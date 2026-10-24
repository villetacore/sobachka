import { useEffect, useState } from "react";
import { api, ApiError, User, ROLE_LABEL } from "./api";
import Orders from "./pages/Orders";
import Profiles from "./pages/Profiles";
import Sources from "./pages/Sources";
import Settings from "./pages/Settings";
import Account from "./pages/Account";
import Users from "./pages/Users";

export default function App() {
  const [user, setUser] = useState<User | null>(null);
  const [checking, setChecking] = useState(true);
  const [error, setError] = useState("");
  const [tab, setTab] = useState("orders");
  const check = () => {
    setChecking(true);
    setError("");
    api<User>("/auth")
      .then(setUser)
      .catch((e) => {
        if (!(e instanceof ApiError && e.status === 401)) setError(e.message);
      })
      .finally(() => setChecking(false));
  };
  useEffect(() => {
    try {
      localStorage.removeItem("sobachka_token");
    } catch {}
    check();
    const expired = () => setUser(null);
    window.addEventListener("session-expired", expired);
    return () => window.removeEventListener("session-expired", expired);
  }, []);
  const logout = async () => {
    try {
      await api("/auth/logout", { method: "POST" });
      setUser(null);
      setTab("orders");
    } catch (e) {
      setError((e as Error).message);
    }
  };
  if (checking) return <div className="center muted">Загрузка…</div>;
  if (!user)
    return error ? (
      <div className="login card">
        <div className="error">{error}</div>
        <button onClick={check}>Повторить</button>
      </div>
    ) : (
      <Login
        onOk={(u) => {
          setTab("orders");
          setUser(u);
        }}
      />
    );
  const tabs = [
    { id: "orders", title: "Заказы" },
    ...(user.role !== "viewer" ? [{ id: "profiles", title: "Профили" }] : []),
    ...(user.role === "admin"
      ? [
          { id: "sources", title: "Источники" },
          { id: "settings", title: "Настройки и ИИ" },
          { id: "users", title: "Пользователи" },
        ]
      : []),
    { id: "account", title: "Мой аккаунт и Telegram" },
  ];
  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">🐶 Собачка</div>
        <nav className="tabs">
          {tabs.map((t) => (
            <button
              key={t.id}
              className={tab === t.id ? "tab active" : "tab"}
              onClick={() => setTab(t.id)}
            >
              {t.title}
            </button>
          ))}
        </nav>
        <span className="small-text">
          {user.username} · {ROLE_LABEL[user.role]}
        </span>
        <button className="ghost small" onClick={logout}>
          Выйти
        </button>
      </header>
      <main className="content">
        {error && <div className="error">{error}</div>}
        {tab === "orders" && <Orders readOnly={user.role === "viewer"} />}
        {tab === "profiles" && user.role !== "viewer" && <Profiles />}
        {tab === "sources" && user.role === "admin" && <Sources />}
        {tab === "settings" && user.role === "admin" && <Settings />}
        {tab === "users" && user.role === "admin" && (
          <Users currentId={user.id} />
        )}{" "}
        {tab === "account" && (
          <Account onPasswordChanged={() => setUser(null)} />
        )}
      </main>
    </div>
  );
}
function Login({ onOk }: { onOk: (u: User) => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      onOk(
        await api<User>("/auth/login", {
          method: "POST",
          body: { username, password },
        }),
      );
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <form className="login card form" onSubmit={submit}>
      <h1>🐶 Собачка</h1>
      <p className="muted">
        Войдите в свой аккаунт. Учётную запись создаёт администратор.
      </p>
      <label>
        Логин
        <input
          autoComplete="username"
          autoFocus
          required
          value={username}
          onChange={(e) => setUsername(e.target.value)}
        />
      </label>
      <label>
        Пароль
        <input
          type="password"
          autoComplete="current-password"
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </label>
      {err && (
        <div className="error" role="alert">
          {err}
        </div>
      )}
      <button className="primary" type="submit" disabled={busy}>
        {busy ? "Входим…" : "Войти"}
      </button>
    </form>
  );
}
