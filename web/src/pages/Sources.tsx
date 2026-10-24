import { useEffect, useState } from "react";
import { api, fmtDate, Source, SourceKind, SourcePreset } from "../api";

const NEW: Source = {
  id: 0,
  kind: "rss",
  name: "",
  url: "",
  enabled: true,
  interval_sec: 180,
  last_run_at: null,
  last_error: "",
  pattern: "",
  cookie: "",
  config: {},
};

// Настройки разбора показываем как JSON: пустой объект — пустое поле.
const configText = (c: Source["config"]) =>
  c && Object.keys(c).length > 0 ? JSON.stringify(c, null, 2) : "";

export default function Sources() {
  const [list, setList] = useState<Source[]>([]);
  const [kinds, setKinds] = useState<SourceKind[]>([]);
  const [presets, setPresets] = useState<SourcePreset[]>([]);
  const [showPresets, setShowPresets] = useState(false);
  const [form, setFormState] = useState<Source | null>(null);
  const [config, setConfig] = useState("");
  const [err, setErr] = useState("");

  const setForm = (s: Source | null) => {
    setFormState(s);
    if (s && s.id !== form?.id) setConfig(configText(s.config));
    if (!s) setConfig("");
  };

  const load = () => api<Source[]>("/sources").then(setList).catch((e) => setErr(e.message));

  useEffect(() => {
    load();
    api<SourceKind[]>("/sources/kinds").then(setKinds).catch(() => {});
    api<SourcePreset[]>("/sources/presets").then(setPresets).catch(() => {});
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  }, []);

  const save = async () => {
    if (!form) return;
    setErr("");
    let parsed: Source["config"] = {};
    try {
      parsed = config.trim() ? JSON.parse(config) : {};
    } catch {
      setErr("Настройки разбора — некорректный JSON");
      return;
    }
    const body = { ...form, config: parsed };
    try {
      if (form.id) await api(`/sources/${form.id}`, { method: "PUT", body });
      else await api("/sources", { method: "POST", body });
      setForm(null);
      load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };

  const toggle = async (s: Source) => {
    await api(`/sources/${s.id}`, { method: "PUT", body: { ...s, enabled: !s.enabled } });
    load();
  };

  const run = async (s: Source) => {
    await api(`/sources/${s.id}/run`, { method: "POST" });
    setTimeout(load, 3000);
  };

  const remove = async (s: Source) => {
    if (!confirm(`Удалить источник «${s.name}» вместе с его заказами?`)) return;
    await api(`/sources/${s.id}`, { method: "DELETE" });
    load();
  };

  const kind = kinds.find((k) => k.id === form?.kind);
  const added = (p: SourcePreset) => list.some((s) => s.url === p.url);

  const usePreset = (p: SourcePreset) => {
    setForm({ ...NEW, kind: p.kind, name: p.name, url: p.url, pattern: p.pattern ?? "", interval_sec: p.interval_sec, config: p.config ?? {} });
    setConfig(configText(p.config ?? {}));
    setShowPresets(false);
  };

  return (
    <div className="stack">
      <div className="toolbar">
        <p className="muted">Биржи, с которых собираются заказы. При первом опросе существующие заказы сохраняются без уведомлений.</p>
        <button onClick={() => setShowPresets(!showPresets)}>Готовые площадки</button>
        <button className="primary" onClick={() => setForm({ ...NEW })}>
          + Добавить источник
        </button>
      </div>
      {err && <div className="error">{err}</div>}

      {showPresets && (
        <div className="table card">
          {presets.length === 0 && (
            <div className="table-row muted small-text">
              Готовых площадок нет. Их список у каждой установки свой: положите его в config/presets.json (пример —
              config/presets.example.json).
            </div>
          )}
          {presets.map((p) => (
            <div key={p.url} className="table-row">
              <div className="grow">
                <div>
                  <b>{p.name}</b> <span className="badge">{p.kind}</span>
                </div>
                <div className="muted small-text">
                  {p.url}
                  {p.note && ` · ${p.note}`}
                </div>
              </div>
              {added(p) ? (
                <span className="muted small-text">уже добавлена</span>
              ) : (
                <button className="small" onClick={() => usePreset(p)}>
                  Добавить
                </button>
              )}
            </div>
          ))}
        </div>
      )}

      {form && (
        <div className="card form">
          <div className="row">
            <label>
              Тип
              <select value={form.kind} onChange={(e) => setForm({ ...form, kind: e.target.value })}>
                {kinds.map((k) => (
                  <option key={k.id} value={k.id}>
                    {k.title}
                  </option>
                ))}
              </select>
            </label>
            <label className="grow">
              Название
              <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="Биржа — раздел" />
            </label>
          </div>
          <label>
            URL
            <input value={form.url} onChange={(e) => setForm({ ...form, url: e.target.value })} placeholder={kind?.example_url} />
          </label>
          {kind && <div className="muted small-text">{kind.hint}</div>}
          {form.kind === "html" && (
            <label>
              Шаблон ссылок на заказы (регулярное выражение)
              <input value={form.pattern} onChange={(e) => setForm({ ...form, pattern: e.target.value })} placeholder="/task/view/[0-9]+" />
            </label>
          )}
          {(form.kind === "json" || form.kind === "html") && (
            <label>
              Настройки разбора (JSON{form.kind === "html" ? ", необязательно" : ""})
              <textarea
                rows={8}
                className="mono"
                value={config}
                onChange={(e) => setConfig(e.target.value)}
                placeholder={
                  form.kind === "json"
                    ? '{"items": "data.list", "fields": {"title": "title", "url": "https://example.com/task/{id}"}}'
                    : '{"card": "<div class=\\"card", "include": "Заказ"}'
                }
              />
            </label>
          )}
          <label>
            Cookie (необязательно — только для страниц, доступных после входа)
            <input value={form.cookie} onChange={(e) => setForm({ ...form, cookie: e.target.value })}
              placeholder="Скопируйте заголовок Cookie из инструментов разработчика браузера" />
          </label>
          <label>
            Интервал опроса, секунд
            <input type="number" min={60} value={form.interval_sec}
              onChange={(e) => setForm({ ...form, interval_sec: Number(e.target.value) || 180 })} />
          </label>
          <div className="actions">
            <button className="primary" onClick={save}>
              Сохранить
            </button>
            <button className="ghost" onClick={() => setForm(null)}>
              Отмена
            </button>
          </div>
        </div>
      )}

      <div className="table card">
        {list.map((s) => (
          <div key={s.id} className="table-row">
            <span className={s.enabled ? "dot on" : "dot"} />
            <div className="grow">
              <div>
                <b>{s.name}</b> <span className="badge">{s.kind}</span>
              </div>
              <div className="muted small-text">
                {s.url} · каждые {Math.round(s.interval_sec / 60)} мин · последний опрос {fmtDate(s.last_run_at)}
              </div>
              {s.last_error && <div className="error small-text">{s.last_error}</div>}
            </div>
            <button className="small" onClick={() => run(s)}>
              Опросить
            </button>
            <button className="small" onClick={() => toggle(s)}>
              {s.enabled ? "Выключить" : "Включить"}
            </button>
            <button className="small" onClick={() => setForm(s)}>
              Изменить
            </button>
            <button className="small danger" onClick={() => remove(s)}>
              ✕
            </button>
          </div>
        ))}
      </div>
    </div>
  );
}
