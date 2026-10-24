import { Fragment, useEffect, useState } from "react";
import { api, fmtDate, MLStatus, pct, Settings as S, BotInfo } from "../api";

export default function Settings() {
  const [s, setS] = useState<S | null>(null);
  const [bot, setBot] = useState<BotInfo | null>(null);
  const [token, setToken] = useState("");
  const [botBusy, setBotBusy] = useState(false);
  const [ml, setMl] = useState<MLStatus | null>(null);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [training, setTraining] = useState(false);

  const loadML = () =>
    api<MLStatus>("/ml/status")
      .then(setMl)
      .catch(() => {});

  useEffect(() => {
    api<{ settings: S; bot: boolean }>("/settings")
      .then((r) => {
        setS(r.settings);
        api<BotInfo>("/telegram/bot")
          .then(setBot)
          .catch((e) => setErr(e.message));
      })
      .catch((e) => setErr(e.message));
    loadML();
    const t = setInterval(loadML, 10000);
    return () => clearInterval(t);
  }, []);

  const wrap = async (fn: () => Promise<unknown>, ok: string) => {
    setErr("");
    setMsg("");
    try {
      await fn();
      setMsg(ok);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };

  const save = () =>
    wrap(() => api("/settings", { method: "PUT", body: s }), "Сохранено");
  const saveBot = async (value: string) => {
    setBotBusy(true);
    await wrap(
      async () => {
        setBot(
          await api<BotInfo>("/telegram/bot", {
            method: "PUT",
            body: { token: value },
          }),
        );
        setToken("");
      },
      value ? "Бот проверен и сохранён" : "Бот отключён",
    );
    setBotBusy(false);
  };
  const train = async () => {
    setTraining(true);
    await wrap(
      () => api("/ml/train", { method: "POST" }),
      "Нейросеть дообучена",
    );
    setTraining(false);
    loadML();
  };

  if (!s)
    return err ? (
      <div className="error">{err}</div>
    ) : (
      <div className="muted">Загрузка…</div>
    );
  const set = <K extends keyof S>(k: K, v: S[K]) => setS({ ...s, [k]: v });
  const h = ml?.health;
  const head = h?.head;

  return (
    <div className="stack narrow">
      <div className="card form">
        <b>Telegram-бот команды</b>
        <p>
          {bot?.id
            ? `Подключён: ${bot.name} (@${bot.username})`
            : "Бот не настроен"}
        </p>
        <label>
          Токен от @BotFather
          <input
            type="password"
            autoComplete="new-password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder="Вставьте токен бота"
          />
        </label>
        <p className="muted small-text">
          Сохранение проверит токен в Telegram. Токен хранится зашифрованным и
          не возвращается в браузер. При подключении другого бота или отключении
          текущего все пользователи должны заново привязать Telegram.
        </p>
        <div className="actions">
          <button
            disabled={botBusy || !token.trim()}
            onClick={() => saveBot(token.trim())}
          >
            {botBusy ? "Проверяю…" : "Проверить и сохранить бота"}
          </button>
          <button
            disabled={botBusy || !bot?.id}
            onClick={() => {
              if (
                window.confirm(
                  "Отключить бота и сбросить все привязки Telegram?",
                )
              )
                saveBot("");
            }}
          >
            Отключить бота
          </button>
        </div>
        <p className="muted">
          Получателя и направления каждый пользователь выбирает в разделе «Мой
          аккаунт и Telegram».
        </p>
        <b>Общие настройки</b>{" "}
        <label className="check">
          <input
            type="checkbox"
            checked={s.notify_enabled}
            onChange={(e) => set("notify_enabled", e.target.checked)}
          />
          Разрешить автоматические уведомления для всей команды
        </label>
        <label className="check">
          <input
            type="checkbox"
            checked={s.auto_pitch}
            onChange={(e) => set("auto_pitch", e.target.checked)}
          />
          Сразу прикладывать отклик по шаблону профиля
        </label>
        <label>
          Не присылать заказы старше, минут
          <input
            type="number"
            min={0}
            value={s.notify_max_age_min}
            onChange={(e) =>
              set("notify_max_age_min", Number(e.target.value) || 0)
            }
          />
        </label>
        <label>
          Дообучать нейросеть после каждых N новых оценок (0 — только вручную)
          <input
            type="number"
            min={0}
            value={s.retrain_every}
            onChange={(e) => set("retrain_every", Number(e.target.value) || 0)}
          />
        </label>
        <div className="actions">
          <button className="primary" onClick={save}>
            Сохранить
          </button>
        </div>
        {msg && <div className="ok">{msg}</div>}
        {err && <div className="error">{err}</div>}
      </div>

      <div className="card form">
        <b>Оценка заказов</b>
        {ml?.error && (
          <div className="error">ML-сервис недоступен: {ml.error}</div>
        )}
        {h && (
          <div className="kv">
            <span>Сервис</span>
            <span>{h.status === "ok" ? "✅ работает" : (h.status ?? "—")}</span>
            <span>Нейросеть</span>
            <span>
              {head?.trained
                ? `обучена на ${head.samples} примерах: ${head.metrics.user_samples ?? 0} ваших 👍/👎 и ${head.metrics.auto_samples ?? 0} размечены сами`
                : "ещё не обучена — работают только ключевые слова"}
            </span>
            {head && (
              <>
                <span>Память</span>
                <span>
                  {head.memory} заказов, новых с прошлого обучения:{" "}
                  {head.new_since_train}
                </span>
              </>
            )}
            {head?.trained &&
              Object.entries(head.profiles ?? {}).map(([name, p]) => (
                <Fragment key={name}>
                  <span className="small-text">{name}</span>
                  <span className="small-text">
                    {p.auto} авто · {p.user} ваших · вес сети {pct(p.weight)}
                  </span>
                </Fragment>
              ))}
            {typeof head?.metrics?.accuracy === "number" && (
              <>
                <span>Точность</span>
                <span>
                  {pct(head.metrics.accuracy)} на {head.metrics.val_size}{" "}
                  {head.metrics.val_on === "user"
                    ? "отложенных ваших оценках"
                    : "отложенных примерах авторазметки"}
                </span>
              </>
            )}
            {head?.trained_at && (
              <>
                <span>Сеть обучена</span>
                <span>{fmtDate(new Date(head.trained_at * 1000).toISOString())}</span>
              </>
            )}
            {ml?.last_train?.at && (
              <>
                <span>Последнее обучение</span>
                <span>
                  {fmtDate(ml.last_train.at)}{" "}
                  {ml.last_train.error && (
                    <span className="error-inline">
                      — {ml.last_train.error}
                    </span>
                  )}
                </span>
              </>
            )}
          </div>
        )}
        <p className="muted small-text">
          Сначала заказы оцениваются по ключевым словам профиля. Своя нейросеть
          (PyTorch, «мешок слов» → MLP) работает на сервере и дообучается сама:
          каждый новый заказ она размечает по ключевым словам, прячет их из
          текста и учится на остальных словах. Так она находит подходящие
          заказы, в которых нет ключевых слов, и отсеивает случайные совпадения.
          Дообучение запускается само раз в {head?.interval_min ?? 30} минут,
          когда появились новые заказы. Внешние сервисы не нужны.
        </p>
        <p className="muted small-text">
          Оценки 👍/👎 необязательны, но каждая весит как три авторазметки и
          поднимает вес сети по направлению: до{" "}
          {pct(head?.self_weight ?? 0.4)} без оценок и до{" "}
          {pct(head?.max_weight ?? 0.7)} после {head?.full_trust ?? 30} оценок.
        </p>
        <div className="actions">
          <button onClick={train} disabled={training}>
            {training ? "Обучаю…" : "Дообучить сейчас"}
          </button>
        </div>
      </div>
    </div>
  );
}
