import { useEffect, useState } from "react";
import { api, User, Role, ROLE_LABEL } from "../api";
export default function Users({ currentId }: { currentId: number }) {
  const [users, setUsers] = useState<User[]>([]);
  const [err, setErr] = useState("");
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Role>("viewer");
  const load = () => api<User[]>("/users").then(setUsers);
  useEffect(() => {
    load().catch((e) => setErr(e.message));
  }, []);
  const save = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setErr("");
    setMsg("");
    try {
      await fn();
      await load();
      setMsg("Сохранено");
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="stack">
      <div className="card form">
        <h2>Пользователи</h2>
        <p className="muted">
          Администратор управляет пользователями, источниками и ботом. Оператор
          редактирует общие профили и работает с заказами. Наблюдатель читает
          заказы. Личные Telegram-подписки доступны всем.
        </p>
        {err && (
          <div className="error" role="alert">
            {err}
          </div>
        )}
        {msg && <div className="ok">{msg}</div>}
        {users.map((u) => (
          <UserRow
            key={u.id}
            user={u}
            self={u.id === currentId}
            busy={busy}
            onSave={(role, enabled, password) =>
              save(() =>
                api(`/users/${u.id}`, {
                  method: "PUT",
                  body: { role, enabled, password },
                }),
              )
            }
          />
        ))}
      </div>
      <form
        className="card form narrow"
        onSubmit={(e) => {
          e.preventDefault();
          save(async () => {
            await api("/users", {
              method: "POST",
              body: { username, password, role, enabled: true },
            });
            setUsername("");
            setPassword("");
          });
        }}
      >
        <h2>Создать пользователя</h2>
        <label>
          Логин
          <input
            required
            minLength={3}
            maxLength={64}
            pattern="[a-zA-Z0-9][a-zA-Z0-9_.\-]{2,63}"
            autoComplete="off"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
          />
        </label>
        <label>
          Пароль (12–72 байта)
          <input
            type="password"
            autoComplete="new-password"
            required
            minLength={12}
            maxLength={72}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        <label>
          Роль
          <select
            value={role}
            onChange={(e) => setRole(e.target.value as Role)}
          >
            {Object.entries(ROLE_LABEL).map(([id, label]) => (
              <option key={id} value={id}>
                {label}
              </option>
            ))}
          </select>
        </label>
        <button className="primary" disabled={busy}>
          Создать
        </button>
      </form>
    </div>
  );
}
function UserRow({
  user,
  self,
  busy,
  onSave,
}: {
  user: User;
  self: boolean;
  busy: boolean;
  onSave: (role: Role, enabled: boolean, password: string) => Promise<void>;
}) {
  const [role, setRole] = useState(user.role);
  const [enabled, setEnabled] = useState(user.enabled);
  const [password, setPassword] = useState("");
  useEffect(() => {
    setRole(user.role);
    setEnabled(user.enabled);
  }, [user]);
  return (
    <form
      className="card form"
      onSubmit={async (e) => {
        e.preventDefault();
        await onSave(role, enabled, password);
        setPassword("");
      }}
    >
      <b>
        {user.username}
        {self ? " (вы)" : ""}
      </b>
      <div className="actions">
        <label>
          Роль
          <select
            value={role}
            onChange={(e) => setRole(e.target.value as Role)}
          >
            {Object.entries(ROLE_LABEL).map(([id, label]) => (
              <option key={id} value={id}>
                {label}
              </option>
            ))}
          </select>
        </label>
        <label className="check">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
          />
          Активен
        </label>
        <label>
          Сброс пароля
          <input
            type="password"
            autoComplete="new-password"
            placeholder="Оставьте пустым, чтобы сохранить"
            minLength={12}
            maxLength={72}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        <button disabled={busy}>Сохранить пользователя</button>
      </div>
      <small className="muted">
        Сохранение завершает все сессии этого пользователя.
        {self && " После сохранения войдите снова."}
      </small>
    </form>
  );
}
