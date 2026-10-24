# Развёртывание на сервере

Базовый запуск через Docker Compose описан в [setup.md](setup.md). Здесь — пример продакшн-схемы:
Proxmox LXC за обратным прокси, исходящий трафик через WireGuard. Готовые файлы лежат в
[`deploy/lxc/`](../deploy/lxc/), секретов в них нет.

## Контейнер

- Debian LXC: 2 vCPU, 3 ГБ RAM, 1 ГБ swap, диск от 20 ГБ, автозапуск.
- Для Docker и VPN в LXC включите `nesting`, `keyctl` и проброс `/dev/net/tun`.
- Проект кладётся в `/opt/sobachka`, рядом `.env` с правами 600.
- Лимиты ресурсов и ротация логов — [`compose.deploy.yml`](../deploy/lxc/compose.deploy.yml):

```bash
docker compose -f docker-compose.yml -f compose.deploy.yml up -d --build
```

## Автозапуск

[`sobachka.service`](../deploy/lxc/sobachka.service) поднимает стек после Docker и WireGuard.
Остановке даётся до 180 секунд — этого хватает PostgreSQL на медленном диске.

```bash
cp deploy/lxc/sobachka.service /etc/systemd/system/
systemctl enable --now sobachka
```

## WireGuard (необязательно)

Если биржи или Telegram с сервера недоступны, исходящий трафик можно пустить через VPN-шлюз.
Скопируйте [`wgsobachka.conf.example`](../deploy/lxc/wgsobachka.conf.example) в `/etc/wireguard/wgsobachka.conf`
и подставьте свои ключи и адрес шлюза. Файлы `deploy/lxc/*.conf` с ключами исключены из Git.
[`wg-network-online.conf`](../deploy/lxc/wg-network-online.conf) — drop-in для `wg-quick@wgsobachka`, чтобы VPN
поднимался после сети.

## Обратный прокси и HTTPS

Подойдёт Caddy, nginx + certbot или Nginx Proxy Manager. Проксируйте домен на `http://<адрес LXC>:8088` и добавьте:

```nginx
proxy_read_timeout 400s;
proxy_send_timeout 400s;
```

`PUBLIC_URL` в `.env` должен совпадать с адресом, который открывает браузер. Если домен за Cloudflare,
режим SSL/TLS — **Full (strict)**.

## SSH

[`ssh-hardening.conf`](../deploy/lxc/ssh-hardening.conf) — drop-in для `/etc/ssh/sshd_config.d/`: вход только по ключу,
без root. Обычному SSH-пользователю не выдавайте sudo и доступ к Docker-сокету — администрируйте через `pct exec`.

## Проверка

```bash
curl -fsS http://<адрес LXC>:8088/api/health
docker compose -f docker-compose.yml -f compose.deploy.yml ps
curl -I https://api.telegram.org
```

После загрузки ML прогревается около двух минут.

## Резервные копии

Сохраняйте дамп PostgreSQL (`docker compose exec postgres pg_dump -U sobachka sobachka`), том `models`
и, отдельно от них, `APP_SECRET_KEY`: без ключа токен бота из резервной копии не расшифровать.
