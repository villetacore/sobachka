import { useEffect, useState } from "react";
import { api, Evaluation, pct, Profile } from "../api";

const EMPTY: Profile = {
  id: 0,
  name: "",
  description: "",
  skills: "",
  keywords: [],
  minus_words: [],
  min_budget: 0,
  threshold: 0.6,
  pitch_notes: "",
  pitch_template: "",
  enabled: true,
};

const splitList = (s: string) =>
  s
    .split(/[,\n]/)
    .map((x) => x.trim())
    .filter(Boolean);

export default function Profiles() {
  const [list, setList] = useState<Profile[]>([]);
  const [edit, setEdit] = useState<Profile | null>(null);
  const [err, setErr] = useState("");

  const load = () =>
    api<Profile[]>("/profiles")
      .then((ps) => {
        setList(ps);
        setEdit((cur) => cur ?? ps[0] ?? { ...EMPTY });
      })
      .catch((e) => setErr(e.message));

  useEffect(() => {
    load();
  }, []);

  return (
    <div className="split">
      <aside className="side card">
        <div className="side-head">
          <b>Профили</b>
          <button className="small" onClick={() => setEdit({ ...EMPTY })}>
            + Новый
          </button>
        </div>
        {list.map((p) => (
          <button key={p.id} className={edit?.id === p.id ? "side-item active" : "side-item"} onClick={() => setEdit(p)}>
            <span className={p.enabled ? "dot on" : "dot"} />
            {p.name}
          </button>
        ))}
        <p className="muted small-text">
          Профиль — одно направление вашей деятельности. Каждый заказ сравнивается со всеми включёнными профилями, выбирается лучший.
        </p>
      </aside>
      <section className="grow">
        {err && <div className="error">{err}</div>}
        {edit && (
          <ProfileEditor
            key={edit.id || "new"}
            initial={edit}
            onSaved={(p) => {
              setEdit(p);
              load();
            }}
            onDeleted={() => {
              setEdit(null);
              load();
            }}
          />
        )}
      </section>
    </div>
  );
}

function ProfileEditor({ initial, onSaved, onDeleted }: { initial: Profile; onSaved: (p: Profile) => void; onDeleted: () => void }) {
  const [p, setP] = useState(initial);
  const [kw, setKw] = useState(initial.keywords.join(", "));
  const [minus, setMinus] = useState(initial.minus_words.join(", "));
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  const current = (): Profile => ({ ...p, keywords: splitList(kw), minus_words: splitList(minus) });
  const set = <K extends keyof Profile>(k: K, v: Profile[K]) => setP((x) => ({ ...x, [k]: v }));

  const save = async () => {
    setErr("");
    setMsg("");
    try {
      const body = current();
      const saved = body.id
        ? await api<Profile>(`/profiles/${body.id}`, { method: "PUT", body })
        : await api<Profile>("/profiles", { method: "POST", body });
      setMsg("Сохранено");
      onSaved(saved);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };

  const remove = async () => {
    if (!p.id || !confirm(`Удалить профиль «${p.name}»?`)) return;
    await api(`/profiles/${p.id}`, { method: "DELETE" });
    onDeleted();
  };

  return (
    <div className="stack">
      <div className="card form">
        <div className="row">
          <label className="grow">
            Название / специализация
            <input value={p.name} onChange={(e) => set("name", e.target.value)} placeholder="Например: Frontend-разработчик React" />
          </label>
          <label className="check">
            <input type="checkbox" checked={p.enabled} onChange={(e) => set("enabled", e.target.checked)} />
            Включён
          </label>
        </div>
        <label>
          Ключевые слова и фразы (через запятую или с новой строки)
          <textarea rows={5} value={kw} onChange={(e) => setKw(e.target.value)} placeholder="сайт, лендинг, верстк, интернет магазин, seo" />
        </label>
        <div className="muted small-text">
          Слово ищется как начало слова: «верстк» найдёт «вёрстка» и «верстку». Слова до 3 букв («seo», «cms») — только
          целиком. Фраза — слова подряд: «интернет магазин». Совпадение в заголовке весит вдвое больше, чем в описании.
        </div>
        <label>
          Минус-слова — такие заказы отбрасываются
          <textarea rows={2} value={minus} onChange={(e) => setMinus(e.target.value)} placeholder="курсов, дипломн, реферат" />
        </label>
        <div className="row">
          <label>
            Мин. бюджет, ₽
            <input type="number" min={0} value={p.min_budget} onChange={(e) => set("min_budget", Number(e.target.value) || 0)} />
          </label>
          <label className="grow">
            Порог уведомления: <b>{pct(p.threshold)}</b>
            <input type="range" min={0.2} max={0.95} step={0.01} value={p.threshold}
              onChange={(e) => set("threshold", Number(e.target.value))} />
          </label>
        </div>
        <label>
          Опыт для отклика — подставляется вместо {"{experience}"}
          <textarea rows={3} value={p.pitch_notes} onChange={(e) => set("pitch_notes", e.target.value)}
            placeholder="Например: 10 лет на производстве, 200+ изготовленных деталей, портфолио: site.ru" />
        </label>
        <label>
          Шаблон отклика заказчику
          <textarea rows={9} value={p.pitch_template} onChange={(e) => set("pitch_template", e.target.value)}
            placeholder="Пусто — будет использован стандартный шаблон" />
        </label>
        <div className="muted small-text">
          Подстановки: {"{title}"} — название заказа, {"{matched}"} — найденные ключевые слова, {"{direction}"} — название профиля,
          {" {experience}"} — опыт, {"{budget}"} — бюджет. Несколько вариантов разделяйте строкой «---», кнопка «Другой вариант»
          переключает их. Хороший отклик: начать с задачи клиента, коротко сказать, как сделаете, и задать один вопрос.
        </div>
        {err && <div className="error">{err}</div>}
        <div className="actions">
          <button className="primary" onClick={save}>
            Сохранить
          </button>
          {p.id > 0 && (
            <button className="danger" onClick={remove}>
              Удалить
            </button>
          )}
          {msg && <span className="ok">{msg}</span>}
        </div>
      </div>
      <Sandbox profile={current} />
    </div>
  );
}

function Sandbox({ profile }: { profile: () => Profile }) {
  const [title, setTitle] = useState("");
  const [desc, setDesc] = useState("");
  const [res, setRes] = useState<{ evaluation: Evaluation; pitch?: string; pitch_error?: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const run = async (generate: boolean) => {
    setBusy(true);
    setErr("");
    try {
      setRes(await api("/profiles/test", { method: "POST", body: { title, description: desc, profile: profile(), generate } }));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const ev = res?.evaluation;
  const reason = ev && Object.values(ev.filtered ?? {})[0];

  return (
    <div className="card form">
      <b>Песочница: проверьте профиль на примере заказа</b>
      <input placeholder="Заголовок заказа" value={title} onChange={(e) => setTitle(e.target.value)} />
      <textarea rows={4} placeholder="Описание заказа" value={desc} onChange={(e) => setDesc(e.target.value)} />
      <div className="actions">
        <button onClick={() => run(false)} disabled={busy || !title}>
          Оценить
        </button>
        <button onClick={() => run(true)} disabled={busy || !title}>
          {busy ? "Работаю…" : "Оценить + показать отклик"}
        </button>
      </div>
      {err && <div className="error">{err}</div>}
      {ev && (
        <div className="sandbox-result">
          {reason ? (
            <div>Отфильтрован: {reason}</div>
          ) : ev.best ? (
            <>
              <div className={ev.matched ? "big good" : "big low"}>
                {pct(ev.best.score)} — {ev.matched ? "будет уведомление" : "ниже порога"}
              </div>
              <div className="muted">
                по ключевым словам {pct(ev.best.base)}
                {ev.best.head != null && <> · обученная нейросеть {pct(ev.best.head)}</>}
                {ev.best.matched_keywords?.length ? <> · ключевые слова: {ev.best.matched_keywords.join(", ")}</> : null}
              </div>
            </>
          ) : null}
          {res?.pitch && <pre className="pitch">{res.pitch}</pre>}
          {res?.pitch_error && <div className="error">{res.pitch_error}</div>}
        </div>
      )}
    </div>
  );
}
