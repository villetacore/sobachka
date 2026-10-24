import { useEffect, useState } from "react";
import { api, BotInfo, Profile, User, ROLE_LABEL } from "../api";
type Preferences = { user: User; profile_ids: number[] };
export default function Account({
  onPasswordChanged,
}: {
  onPasswordChanged: () => void;
}) {
  const [p, setP] = useState<Preferences | null>(null);
  const [bot, setBot] = useState<BotInfo | null>(null);
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [link, setLink] = useState("");
  const [err, setErr] = useState("");
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);
  const [old, setOld] = useState("");
  const [password, setPassword] = useState("");
  const load = async () => {
    const [p, b, ps] = await Promise.all([
      api<Preferences>("/me/notifications"),
      api<BotInfo>("/telegram/bot"),
      api<Profile[]>("/profiles"),
    ]);
    setP(p);
    setBot(b);
    setProfiles(ps);
  };
  useEffect(() => {
    load().catch((e) => setErr(e.message));
  }, []);
  const run = async (fn: () => Promise<unknown>, message: string) => {
    setBusy(true);
    setErr("");
    setMsg("");
    try {
      await fn();
      setMsg(message);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  if (!p) return <div className="card">{err || "Загрузка…"}</div>;
  const setUser = (patch: Partial<User>) =>
    setP({ ...p, user: { ...p.user, ...patch } });
  return (
    <div className="stack narrow">
      <div className="card form">
        <h2>Мой Telegram</h2>
        <p>
          {p.user.username} · {ROLE_LABEL[p.user.role]}
        </p>
        <p className="muted">
          Заказы, профили и оценки общие для команды. Получатель и подписки на
          уведомления — личные.
        </p>
        <p>
          {bot?.id ? (
            <>
              Бот:{" "}
              <a
                href={`https://t.me/${bot.username}`}
                target="_blank"
                rel="noreferrer"
              >
                @{bot.username}
              </a>{" "}
              · {bot.name}
            </>
          ) : (
            "Бот ещё не настроен администратором."
          )}
        </p>
        <p>
          {p.user.telegram_chat_id
            ? `✓ Telegram подключён (чат ${p.user.telegram_chat_id})`
            : "Telegram не подключён"}
        </p>
        <div className="actions">
          <button
            disabled={busy || !bot?.id}
            onClick={() =>
              run(async () => {
                const r = await api<{ url: string }>("/me/telegram/link", {
                  method: "POST",
                });
                setLink(r.url);
              }, "Ссылка действует 10 минут. Откройте её и нажмите Start в Telegram.")
            }
          >
            {p.user.telegram_chat_id
              ? "Переподключить Telegram"
              : "Подключить Telegram"}
          </button>
          <button disabled={busy} onClick={() => run(load, "Статус обновлён")}>
            Обновить статус
          </button>
          {p.user.telegram_chat_id && (
            <button
              disabled={busy}
              onClick={() =>
                run(async () => {
                  await api("/me/telegram/link", { method: "DELETE" });
                  setLink("");
                  await load();
                }, "Telegram отключён")
              }
            >
              Отключить
            </button>
          )}
        </div>
        {link && (
          <p>
            <a href={link} target="_blank" rel="noreferrer">
              Открыть личную ссылку в Telegram ↗
            </a>
            <br />
            <small>
              Не передавайте ссылку другим: она привязывает Telegram к вашему
              аккаунту. Новая ссылка отменяет предыдущую.
            </small>
          </p>
        )}
        <label className="check">
          <input
            type="checkbox"
            checked={p.user.notify_enabled}
            onChange={(e) => setUser({ notify_enabled: e.target.checked })}
          />
          Получать уведомления
        </label>
        <label className="check">
          <input
            type="checkbox"
            checked={p.user.all_profiles}
            onChange={(e) => setUser({ all_profiles: e.target.checked })}
          />
          Все направления, включая новые
        </label>
        {!p.user.all_profiles && (
          <div className="stack">
            {profiles.map((profile) => (
              <label className="check" key={profile.id}>
                <input
                  type="checkbox"
                  checked={p.profile_ids.includes(profile.id)}
                  onChange={(e) =>
                    setP({
                      ...p,
                      profile_ids: e.target.checked
                        ? [...p.profile_ids, profile.id]
                        : p.profile_ids.filter((id) => id !== profile.id),
                    })
                  }
                />
                {profile.name}
                {!profile.enabled && " (выключен)"}
              </label>
            ))}
            {p.profile_ids.length === 0 && (
              <p className="muted">
                Выберите хотя бы одно направление, иначе уведомлений не будет.
              </p>
            )}
          </div>
        )}
        <p className="muted small-text">
          Подписки проверяются по лучшему профилю заказа. Общий переключатель
          уведомлений у администратора также должен быть включён.
        </p>
        <div className="actions">
          <button
            className="primary"
            disabled={busy}
            onClick={() =>
              run(
                () =>
                  api("/me/notifications", {
                    method: "PUT",
                    body: {
                      notify_enabled: p.user.notify_enabled,
                      all_profiles: p.user.all_profiles,
                      profile_ids: p.profile_ids,
                    },
                  }),
                "Настройки уведомлений сохранены",
              )
            }
          >
            Сохранить подписки
          </button>
          <button
            disabled={busy || !p.user.telegram_chat_id || !bot?.id}
            onClick={() =>
              run(
                () => api("/me/telegram/test", { method: "POST" }),
                "Тестовое сообщение отправлено в ваш Telegram",
              )
            }
          >
            Отправить тест себе
          </button>
        </div>
        {msg && (
          <div className="ok" role="status">
            {msg}
          </div>
        )}
        {err && (
          <div className="error" role="alert">
            {err}
          </div>
        )}
      </div>
      <form
        className="card form"
        onSubmit={(e) => {
          e.preventDefault();
          run(async () => {
            await api("/auth/password", {
              method: "POST",
              body: { current_password: old, password },
            });
            onPasswordChanged();
          }, "");
        }}
      >
        <h2>Сменить пароль</h2>
        <label>
          Текущий пароль
          <input
            type="password"
            autoComplete="current-password"
            required
            value={old}
            onChange={(e) => setOld(e.target.value)}
          />
        </label>
        <label>
          Новый пароль (12–72 байта)
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
        <p className="muted">
          После смены пароля все ваши сессии завершатся. Войдите снова.
        </p>
        <button className="primary" disabled={busy}>
          Сменить пароль
        </button>
      </form>
    </div>
  );
}
