import { useCallback, useEffect, useState } from "react";
import { api, fmtDate, Order, pct, Profile, Stats, STATUS_LABEL } from "../api";

const PAGE = 30;

export default function Orders({ readOnly = false }: { readOnly?: boolean }) {
  const [items, setItems] = useState<Order[]>([]);
  const [total, setTotal] = useState(0);
  const [stats, setStats] = useState<Stats | null>(null);
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [status, setStatus] = useState("");
  const [profileId, setProfileId] = useState("");
  const [q, setQ] = useState("");
  const [err, setErr] = useState("");
  const [loading, setLoading] = useState(false);

  const load = useCallback(
    async (offset = 0) => {
      setLoading(true);
      setErr("");
      try {
        const params = new URLSearchParams({
          limit: String(PAGE),
          offset: String(offset),
        });
        if (status) params.set("status", status);
        if (profileId) params.set("profile_id", profileId);
        if (q.trim()) params.set("q", q.trim());
        const res = await api<{ items: Order[]; total: number }>(
          `/orders?${params}`,
        );
        setItems((prev) => (offset ? [...prev, ...res.items] : res.items));
        setTotal(res.total);
        setStats(await api<Stats>("/stats"));
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
      } finally {
        setLoading(false);
      }
    },
    [status, profileId, q],
  );

  useEffect(() => {
    api<Profile[]>("/profiles")
      .then(setProfiles)
      .catch(() => {});
  }, []);

  useEffect(() => {
    const t = setTimeout(() => load(0), 250);
    return () => clearTimeout(t);
  }, [load]);

  const replace = (o: Order) =>
    setItems((prev) => prev.map((x) => (x.id === o.id ? o : x)));

  return (
    <div className="stack">
      {stats && (
        <div className="stats">
          <Stat label="Всего" value={stats.total} />
          <Stat label="За 24 часа" value={stats.today} />
          <Stat label="Подошли" value={stats.matched} />
          <Stat label="Отправлено в TG" value={stats.notified} />
          <Stat
            label="👍 / 👎"
            value={`${stats.positive} / ${stats.negative}`}
          />
          {stats.unscored > 0 && (
            <Stat label="В очереди" value={stats.unscored} />
          )}
          {stats.errors > 0 && (
            <Stat label="Ошибки" value={stats.errors} warn />
          )}
        </div>
      )}

      <div className="filters">
        <input
          placeholder="Поиск по тексту…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <select value={status} onChange={(e) => setStatus(e.target.value)}>
          <option value="">Все статусы</option>
          {Object.entries(STATUS_LABEL).map(([k, v]) => (
            <option key={k} value={k}>
              {v}
            </option>
          ))}
        </select>
        <select
          value={profileId}
          onChange={(e) => setProfileId(e.target.value)}
        >
          <option value="">Все профили</option>
          {profiles.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
        <button onClick={() => load(0)} disabled={loading}>
          Обновить
        </button>
      </div>

      {err && <div className="error">{err}</div>}
      {!loading && items.length === 0 && !err && (
        <div className="empty card">
          Заказов пока нет. Первые появятся через пару минут после запуска —
          источники опрашиваются по расписанию.
        </div>
      )}

      {items.map((o) => (
        <OrderCard
          key={o.id}
          order={o}
          onChange={replace}
          readOnly={readOnly}
        />
      ))}

      {items.length < total && (
        <button
          className="more"
          onClick={() => load(items.length)}
          disabled={loading}
        >
          Показать ещё ({total - items.length})
        </button>
      )}
    </div>
  );
}

function Stat({
  label,
  value,
  warn,
}: {
  label: string;
  value: number | string;
  warn?: boolean;
}) {
  return (
    <div className={warn ? "stat warn" : "stat"}>
      <div className="stat-value">{value}</div>
      <div className="stat-label">{label}</div>
    </div>
  );
}

function scoreClass(o: Order) {
  if (o.score == null) return "score none";
  if (o.status === "matched" || o.status === "notified") return "score good";
  return o.score >= 0.4 ? "score mid" : "score low";
}

function OrderCard({
  order: o,
  onChange,
  readOnly,
}: {
  order: Order;
  onChange: (o: Order) => void;
  readOnly: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");
  const [copied, setCopied] = useState(false);

  const act = async (name: string, path: string, body?: unknown) => {
    setBusy(name);
    setErr("");
    try {
      onChange(
        await api<Order>(`/orders/${o.id}/${path}`, {
          method: "POST",
          body: body ?? {},
        }),
      );
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy("");
    }
  };

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(o.pitch);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* нет доступа к буферу обмена */
    }
  };

  return (
    <article
      className={`card order ${o.feedback > 0 ? "liked" : o.feedback < 0 ? "disliked" : ""}`}
    >
      <div className="order-head" onClick={() => setOpen(!open)}>
        <div className={scoreClass(o)} title="Совпадение с профилем">
          {pct(o.score)}
        </div>
        <div className="order-main">
          <div className="order-title">{o.title}</div>
          <div className="order-meta">
            <span>{o.source_name}</span>
            {o.budget && <span>💰 {o.budget}</span>}
            {o.profile_name && <span>👤 {o.profile_name}</span>}
            {o.matched && o.matched.length > 0 && (
              <span>🔑 {o.matched.slice(0, 5).join(", ")}</span>
            )}
            <span>{fmtDate(o.published_at ?? o.created_at)}</span>
            <span className={`badge ${o.status}`}>
              {STATUS_LABEL[o.status]}
            </span>
          </div>
        </div>
      </div>

      {open && (
        <div className="order-body">
          {o.description && <p className="desc">{o.description}</p>}
          {o.error && <div className="error">{o.error}</div>}

          <div className="pitch-box">
            <div className="pitch-head">
              <b>Отклик</b>
              {o.pitch && (
                <button className="ghost small" onClick={copy}>
                  {copied ? "Скопировано ✓" : "Копировать"}
                </button>
              )}
            </div>
            {o.pitch ? (
              <pre className="pitch">{o.pitch}</pre>
            ) : (
              <div className="muted">Ещё не составлен.</div>
            )}
          </div>

          {err && <div className="error">{err}</div>}
          <div className="actions">
            <a
              className="button primary"
              href={o.url}
              target="_blank"
              rel="noreferrer"
            >
              Открыть заказ ↗
            </a>
            <button
              onClick={() => act("gen", "generate")}
              disabled={!!busy || readOnly}
            >
              {o.pitch ? "🔄 Другой вариант" : "✍️ Составить отклик"}
            </button>
            <button
              onClick={() => act("tg", "notify")}
              disabled={!!busy || readOnly}
            >
              {busy === "tg" ? "Отправляю…" : "📨 В мой Telegram"}
            </button>
            <button
              onClick={() => act("rescore", "rescore")}
              disabled={!!busy || readOnly}
              title="Пересчитать совпадение с текущими профилями"
            >
              {busy === "rescore" ? "Считаю…" : "🧠 Переоценить"}
            </button>
            <span className="spacer" />
            <button
              className={o.feedback > 0 ? "fb on" : "fb"}
              onClick={() =>
                act("fb", "feedback", { value: o.feedback > 0 ? 0 : 1 })
              }
              disabled={!!busy || readOnly}
              title="Подходит — нейросеть запомнит"
            >
              👍
            </button>
            <button
              className={o.feedback < 0 ? "fb on" : "fb"}
              onClick={() =>
                act("fb", "feedback", { value: o.feedback < 0 ? 0 : -1 })
              }
              disabled={!!busy || readOnly}
              title="Не подходит — нейросеть запомнит"
            >
              👎
            </button>
          </div>
        </div>
      )}
    </article>
  );
}
