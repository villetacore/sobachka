"""Оценка релевантности заказа профилю.

Две ступени, без внешних предобученных моделей и сторонних API:
1. Ключевые слова и фразы профиля. Каждое слово ищется как начало слова в тексте
   («сайт» → «сайта», «сайтов»); слова до 3 букв («seo», «cms») — только целиком.
   Совпадение в заголовке весит вдвое больше, чем в описании.
2. Собственная нейросеть (MLP на PyTorch) поверх «мешка слов» заказа. Она дообучается сама:
   каждый заказ, который видит сервис, размечается по ключевым словам (явное совпадение —
   «подходит», ни одного — «мимо»), а сами ключевые слова при обучении из текста прячутся.
   Так сеть учит слова-спутники и узнаёт подходящие заказы, где ключевого слова нет.
   Оценки 👍/👎 добавляются к авторазметке с весом втрое больше и со временем перевешивают её.
"""
import hashlib
import json
import logging
import math
import os
import random
import re
import threading
import time
from collections import OrderedDict
from pathlib import Path

import torch
from torch import nn

log = logging.getLogger("scorer")

MODEL_DIR = Path(os.getenv("MODEL_DIR", "/models"))
HEAD_PATH = MODEL_DIR / "relevance_bow.pt"
HASH_DIM = 8192  # размер «мешка слов» (хеширование слов и пар слов)
PROFILE_DIM = 64  # хешированный идентификатор профиля
EXTRA = 3  # скор по ключевым словам, совпадения в заголовке, в описании
# score = 1 - exp(-KW_RATE * вес); вес: заголовок = 2, описание = 1.
# Одно слово в описании → 55%, в заголовке → 80%, три совпадения → ~90%.
KW_RATE = float(os.getenv("KW_RATE", "0.8"))
# Вес нейросети считается по каждому профилю отдельно: пока по направлению мало оценок,
# решают ключевые слова (иначе сеть, обученная на заказах одного направления, занижала бы остальные).
HEAD_FULL_TRUST = int(os.getenv("HEAD_FULL_TRUST", "30"))
HEAD_MAX_WEIGHT = float(os.getenv("HEAD_MAX_WEIGHT", "0.7"))
# Самообучение: сколько заказов помнить, как часто и после скольких новых заказов дообучаться.
SEEN_PATH = MODEL_DIR / "seen_orders.jsonl"
LABELS_PATH = MODEL_DIR / "labels.json"
SEEN_MAX = int(os.getenv("SELF_TRAIN_MEMORY", "5000"))
SELF_TRAIN_INTERVAL = int(os.getenv("SELF_TRAIN_INTERVAL_MIN", "30")) * 60
SELF_TRAIN_MIN_NEW = int(os.getenv("SELF_TRAIN_MIN_NEW", "20"))
# Вес сети, обученной только на авторазметке (по направлению, где нашлось хотя бы SELF_MIN_POS примеров).
SELF_MAX_WEIGHT = float(os.getenv("SELF_MAX_WEIGHT", "0.4"))
SELF_MIN_POS = 5
USER_WEIGHT = 3.0  # оценка 👍/👎 весит как три примера авторазметки
MAX_AUTO = 4000  # потолок примеров авторазметки за одно обучение (память и время)

WORD_RE = re.compile(r"[a-zа-я0-9]+")


def normalize(text: str) -> str:
    return (text or "").lower().replace("ё", "е")


def stem(word: str) -> str:
    # Грубый стемминг для «мешка слов»: первых 6 букв достаточно, чтобы
    # «разработка», «разработать» и «разработчик» попали в один признак.
    return word[:6]


def tokens(text: str) -> list[str]:
    return [stem(w) for w in WORD_RE.findall(normalize(text)) if len(w) > 1]


def _hash(s: str, dim: int) -> int:
    return int(hashlib.md5(s.encode()).hexdigest()[:8], 16) % dim


# ---------- ключевые слова ----------

_pattern_cache: dict[str, re.Pattern] = {}


def keyword_pattern(kw: str) -> re.Pattern | None:
    kw = normalize(kw).strip()
    if kw in _pattern_cache:
        return _pattern_cache[kw]
    words = [w for w in re.split(r"[\s\-]+", kw) if w]
    if not words:
        return None
    parts = []
    for w in words:
        w = re.escape(w)
        parts.append(w + (r"(?![a-zа-я0-9])" if len(w) <= 3 else r"[a-zа-я0-9]*"))
    pat = re.compile(r"(?<![a-zа-я0-9])" + r"[\s\-]+".join(parts))
    _pattern_cache[kw] = pat
    return pat


def match_keywords(title: str, description: str, keywords: list[str]) -> tuple[list[str], int, int]:
    """Возвращает (совпавшие ключевые слова, сколько в заголовке, сколько только в описании)."""
    t, d = normalize(title), normalize(description)
    found, in_title, in_desc = [], 0, 0
    seen = set()
    for kw in keywords or []:
        # «интернет-магазин» и «интернет магазин» — один и тот же ключ, считаем один раз.
        key = " ".join(re.split(r"[\s\-]+", normalize(kw).strip()))
        if not key or key in seen:
            continue
        seen.add(key)
        pat = keyword_pattern(kw)
        if pat is None:
            continue
        # Возвращаем слово так, как оно написано в заказе («верстку», а не ключ «верстк»):
        # оно показывается в Telegram и подставляется в отклик вместо {matched}.
        m = pat.search(t)
        if m:
            in_title += 1
        else:
            m = pat.search(d)
            if m:
                in_desc += 1
        if m and m.group(0) not in found:
            found.append(m.group(0))
    return found, in_title, in_desc


def keyword_score(in_title: int, in_desc: int) -> float:
    return 1 - math.exp(-KW_RATE * (2 * in_title + in_desc))


# ---------- нейросеть ----------

class RelevanceNet(nn.Module):
    def __init__(self):
        super().__init__()
        self.net = nn.Sequential(
            nn.Linear(HASH_DIM + PROFILE_DIM + EXTRA, 64),
            nn.ReLU(),
            nn.Dropout(0.3),
            nn.Linear(64, 1),
        )

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return self.net(x).squeeze(-1)


def keyword_stems(keywords: list[str]) -> tuple[set[str], tuple[str, ...]]:
    """Стемы слов из ключевых слов: короткие — для точного сравнения, длинные — как префиксы."""
    exact, prefixes = set(), []
    for kw in keywords or []:
        for w in WORD_RE.findall(normalize(kw)):
            if len(w) <= 3:
                exact.add(w)
            else:
                prefixes.append(stem(w))
    return exact, tuple(prefixes)


def features(title: str, description: str, category: str, profile_name: str,
             kw: float, in_title: int, in_desc: int, mask: list[str] | None = None) -> torch.Tensor:
    """mask — ключевые слова, которые нужно спрятать из «мешка слов» (для авторазметки)."""
    x = torch.zeros(HASH_DIM + PROFILE_DIM + EXTRA)
    exact, prefixes = keyword_stems(mask) if mask else (set(), ())
    # Каждое слово даёт два признака: общий («заказ из нашей сферы») и свой для направления —
    # иначе сеть не отличает соседние направления: у них похожая лексика.
    pn = normalize(profile_name) + "|"
    for weight, text in ((2.0, title), (1.0, (description or "")[:3000]), (1.0, category)):
        toks = tokens(text)
        if mask:
            toks = [t for t in toks if t not in exact and not t.startswith(prefixes)]
        for i, tok in enumerate(toks):
            x[_hash(tok, HASH_DIM)] += weight * 0.5
            x[_hash(pn + tok, HASH_DIM)] += weight
            if i + 1 < len(toks):
                x[_hash(pn + tok + " " + toks[i + 1], HASH_DIM)] += weight * 0.5
    bag = x[:HASH_DIM]
    bag.log1p_()
    norm = bag.norm()
    if norm > 0:
        bag /= norm
    x[HASH_DIM + _hash(normalize(profile_name), PROFILE_DIM)] = 1.0
    x[-3:] = torch.tensor([kw, min(in_title, 3) / 3, min(in_desc, 3) / 3])
    return x


class Scorer:
    def __init__(self):
        self.net: RelevanceNet | None = None
        self.samples = 0
        self.user_counts: dict[str, int] = {}
        self.auto_counts: dict[str, int] = {}
        self.metrics: dict = {}
        self.trained_at = 0.0
        self.lock = threading.Lock()  # подмена сети
        self.train_lock = threading.Lock()  # одно обучение за раз
        self.seen_lock = threading.Lock()
        # Последние увиденные заказы (для авторазметки) и актуальные ключевые слова профилей.
        self.seen: OrderedDict[str, dict] = OrderedDict()
        self.seen_lines = 0
        self.new_seen = 0
        self.profiles: dict[str, list[str]] = {}
        self.labels: list[dict] = []
        self._load()

    # ---------- состояние на диске ----------

    def _load(self):
        if HEAD_PATH.exists():
            try:
                ckpt = torch.load(HEAD_PATH, map_location="cpu", weights_only=False)
                if "auto_counts" not in ckpt:
                    # Сеть старого формата видела ключевые слова в тексте — несовместима, обучим заново.
                    raise ValueError("старый формат чекпоинта")
                net = RelevanceNet()
                net.load_state_dict(ckpt["state"])
                net.eval()
                self.net, self.samples, self.metrics = net, ckpt.get("samples", 0), ckpt.get("metrics", {})
                # Старые чекпоинты хранили только счётчики оценок пользователя.
                self.user_counts, self.auto_counts = ckpt.get("user_counts", {}), ckpt["auto_counts"]
                self.trained_at = ckpt.get("trained_at", 0.0)
                log.info("загружена обученная нейросеть: %d примеров", self.samples)
            except Exception:
                log.exception("не удалось загрузить нейросеть, начинаю с нуля")
        try:
            if LABELS_PATH.exists():
                data = json.loads(LABELS_PATH.read_text())
                self.labels, self.profiles = data.get("labels", []), data.get("profiles", {})
        except Exception:
            log.exception("не удалось прочитать сохранённые оценки")
        if SEEN_PATH.exists():
            with SEEN_PATH.open(encoding="utf-8") as f:
                for line in f:
                    try:
                        o = json.loads(line)
                    except ValueError:
                        continue
                    self._remember(o)
                    self.seen_lines += 1
            log.info("в памяти %d заказов для самообучения", len(self.seen))

    def _save_labels(self):
        MODEL_DIR.mkdir(parents=True, exist_ok=True)
        tmp = LABELS_PATH.with_suffix(".tmp")
        tmp.write_text(json.dumps({"labels": self.labels, "profiles": self.profiles}, ensure_ascii=False))
        tmp.replace(LABELS_PATH)

    @staticmethod
    def _key(o: dict) -> str:
        return hashlib.md5((normalize(o.get("title", "")) + "|" + normalize(o.get("description", ""))[:300]).encode()).hexdigest()

    def _remember(self, o: dict) -> bool:
        k = self._key(o)
        new = k not in self.seen
        self.seen[k] = o
        self.seen.move_to_end(k)
        while len(self.seen) > SEEN_MAX:
            self.seen.popitem(last=False)
        return new

    def observe(self, orders: list[dict]):
        """Запоминает заказы для самообучения (повторы не считаются новыми)."""
        lines = []
        with self.seen_lock:
            for o in orders:
                o = {"title": o.get("title", "") or "", "description": (o.get("description", "") or "")[:3000],
                     "category": o.get("category", "") or ""}
                if not o["title"]:
                    continue
                if self._remember(o):
                    self.new_seen += 1
                    lines.append(json.dumps(o, ensure_ascii=False))
            if not lines:
                return
            MODEL_DIR.mkdir(parents=True, exist_ok=True)
            if self.seen_lines + len(lines) > 2 * SEEN_MAX:
                # Файл только дописывается — время от времени сжимаем его до актуального окна.
                tmp = SEEN_PATH.with_suffix(".tmp")
                with tmp.open("w", encoding="utf-8") as f:
                    for v in self.seen.values():
                        f.write(json.dumps(v, ensure_ascii=False) + "\n")
                tmp.replace(SEEN_PATH)
                self.seen_lines = len(self.seen)
            else:
                with SEEN_PATH.open("a", encoding="utf-8") as f:
                    f.write("\n".join(lines) + "\n")
                self.seen_lines += len(lines)

    # ---------- оценка ----------

    def _weight(self, profile_name: str) -> float:
        if self.net is None:
            return 0.0
        name = normalize(profile_name)
        user = HEAD_MAX_WEIGHT * min(1.0, self.user_counts.get(name, 0) / HEAD_FULL_TRUST)
        auto = SELF_MAX_WEIGHT if self.auto_counts.get(name, 0) >= SELF_MIN_POS else 0.0
        return max(user, auto)

    def info(self) -> dict:
        names = sorted(set(self.user_counts) | set(self.auto_counts))
        return {"trained": self.net is not None, "samples": self.samples,
                "max_weight": HEAD_MAX_WEIGHT, "full_trust": HEAD_FULL_TRUST, "self_weight": SELF_MAX_WEIGHT,
                "profiles": {n: {"user": self.user_counts.get(n, 0), "auto": self.auto_counts.get(n, 0),
                                 "weight": round(self._weight(n), 3)} for n in names},
                "metrics": self.metrics, "memory": len(self.seen), "new_since_train": self.new_seen,
                "trained_at": self.trained_at or None, "interval_min": SELF_TRAIN_INTERVAL // 60}

    def score(self, order: dict, profiles: list[dict]) -> list[dict]:
        title, desc, cat = order.get("title", ""), order.get("description", ""), order.get("category", "")
        self.observe([order])
        for p in profiles:
            if p.get("name") and p.get("keywords"):
                self.profiles[normalize(p["name"])] = list(p["keywords"])
        with self.lock:
            net = self.net
        out = []
        for p in profiles:
            weight = self._weight(p.get("name", ""))
            found, nt, nd = match_keywords(title, desc, p.get("keywords") or [])
            base = keyword_score(nt, nd)
            head = None
            final = base
            # Сеть уточняет только заказы, где есть хоть одно совпадение: поднимает похожие на настоящие
            # и опускает случайные. Заказы совсем без совпадений она не трогает — там она лишь гадала бы.
            if net is not None and weight > 0 and base > 0:
                # Сеть смотрит на текст без ключевых слов: их уже учла первая ступень.
                x = features(title, desc, cat, p.get("name", ""), 0.0, 0, 0, mask=p.get("keywords"))
                with torch.inference_mode():
                    head = float(torch.sigmoid(net(x.unsqueeze(0)))[0])
                final = (1 - weight) * base + weight * head
            out.append({
                "profile_id": p.get("id", 0),
                "score": round(final, 4),
                "base": round(base, 4),
                "head": None if head is None else round(head, 4),
                "matched_keywords": found,
            })
        return out

    # ---------- обучение ----------

    def train(self, examples: list[dict], unlabeled: list[dict] | None = None,
              profiles: list[dict] | None = None) -> dict:
        """Обучение по запросу бэкенда: оценки 👍/👎, свежие заказы и текущие профили."""
        if profiles is not None:
            self.profiles = {normalize(p["name"]): list(p.get("keywords") or []) for p in profiles if p.get("name")}
        self.labels = examples
        self._save_labels()
        if unlabeled:
            self.observe(unlabeled)
        return self.self_train()

    def maybe_self_train(self):
        """Вызывается фоновым потоком: дообучиться, если накопились новые заказы."""
        if self.new_seen < SELF_TRAIN_MIN_NEW or not self.profiles:
            return
        try:
            self.self_train()
        except ValueError as e:
            log.info("самообучение пропущено: %s", e)

    def _auto_labels(self) -> tuple[list[dict], list[dict]]:
        """Авторазметка запомненных заказов по ключевым словам: (уверенные, спорные)."""
        user_keys = {self._key(e) for e in self.labels}
        with self.seen_lock:
            orders = [o for o in self.seen.values() if self._key(o) not in user_keys]
        profiles = [(n, k) for n, k in list(self.profiles.items()) if k]
        # Для каждого заказа и направления: 1 — подходит, 0 — ни одного совпадения, None — спорно.
        marks = []
        for o in orders:
            row = {}
            for name, kws in profiles:
                _, nt, nd = match_keywords(o["title"], o["description"], kws)
                row[name] = 1 if nt >= 1 or nd >= 2 else 0 if nt + nd == 0 else None
            marks.append(row)
        rnd = random.Random(42)
        sure, unsure = [], []
        for name, kws in profiles:
            pos, hard, neg, mid = [], [], [], []
            for o, row in zip(orders, marks):
                ex = {**o, "profile_name": name, "profile_keywords": kws, "origin": "auto"}
                m = row[name]
                if m == 1:
                    pos.append({**ex, "label": 1})
                elif m is None:
                    mid.append(ex)
                elif any(v == 1 for v in row.values()):
                    hard.append(ex)  # подходит другому направлению — самый полезный пример «мимо»
                else:
                    neg.append(ex)
            # Заказов «мимо» намного больше — берём до десяти на каждый подходящий, начиная с соседних направлений.
            rnd.shuffle(hard)
            rnd.shuffle(neg)
            limit = max(10 * len(pos), 200)
            sure += pos + [{**e, "label": 0} for e in (hard + neg)[:limit]]
            unsure += mid
        rnd.shuffle(sure)
        rnd.shuffle(unsure)
        return sure[:MAX_AUTO], unsure[:MAX_AUTO // 4]

    @staticmethod
    def _row(e: dict) -> torch.Tensor:
        # Метки авторазметки выведены из ключевых слов — прячем их (и признаки совпадений),
        # иначе сеть выучит само правило разметки, а не слова-спутники. Так же сеть смотрит
        # на заказ и при оценке, поэтому то же делаем и для оценок пользователя.
        return features(e.get("title", ""), e.get("description", ""), e.get("category", ""),
                        e.get("profile_name", ""), 0.0, 0, 0, mask=e.get("profile_keywords"))

    def self_train(self) -> dict:
        if not self.train_lock.acquire(blocking=False):
            raise ValueError("обучение уже идёт")
        try:
            return self._self_train()
        finally:
            self.train_lock.release()

    def _self_train(self) -> dict:
        started = time.time()
        self.new_seen = 0
        user = [{**e, "origin": "user", "label": 1 if int(e.get("label", 0)) > 0 else 0} for e in self.labels]
        random.Random(1).shuffle(user)
        sure, unsure = self._auto_labels()
        data = user + sure
        if not any(e["label"] for e in data) or all(e["label"] for e in data):
            raise ValueError("пока не из чего учиться: нужны заказы и с совпадениями, и без них")

        def tensors(rows: list[dict], weight: float | None = None):
            X = torch.stack([self._row(e) for e in rows])
            y = torch.tensor([float(e["label"]) for e in rows])
            w = torch.tensor([USER_WEIGHT if e["origin"] == "user" else (weight or 1.0) for e in rows])
            return X, y, w

        # Проверка качества: на отложенных оценках пользователя, если их достаточно, иначе на авторазметке.
        metrics: dict = {"val_size": 0, "accuracy": None, "val_on": None}
        val_pool, val_on = (user, "user") if len(user) >= 10 else (sure, "auto")
        if len(val_pool) >= 30 or val_on == "user":
            n_val = max(5, len(val_pool) // 5)
            val = val_pool[:n_val]
            val_ids = {id(v) for v in val}
            train_rows = [e for e in data if id(e) not in val_ids]
            if len({e["label"] for e in train_rows}) == 2:
                X, y, w = tensors(train_rows)
                probe, _ = self._fit(X, y, w)
                Xv, yv, _ = tensors(val)
                with torch.inference_mode():
                    pred = (torch.sigmoid(probe(Xv)) >= 0.5).float()
                metrics.update(accuracy=round(float((pred == yv).float().mean()), 4), val_size=len(val), val_on=val_on)

        X, y, w = tensors(data)
        net, loss = self._fit(X, y, w)

        # Второй круг самообучения: спорные заказы (одно слово в описании) размечает сама сеть,
        # если уверена, и учится на них с половинным весом.
        extra = []
        if unsure:
            Xu = torch.stack([self._row(e) for e in unsure])
            with torch.inference_mode():
                p = torch.sigmoid(net(Xu)).tolist()
            extra = [{**e, "label": 1 if pi > 0.5 else 0} for e, pi in zip(unsure, p) if pi >= 0.85 or pi <= 0.15]
            if extra:
                X2, y2, w2 = tensors(extra, weight=0.5)
                net, loss = self._fit(torch.cat([X, X2]), torch.cat([y, y2]), torch.cat([w, w2]))

        user_counts: dict[str, int] = {}
        for e in user:
            k = normalize(e.get("profile_name", ""))
            user_counts[k] = user_counts.get(k, 0) + 1
        auto_counts: dict[str, int] = {}
        for e in sure + extra:
            if e["label"]:
                auto_counts[e["profile_name"]] = auto_counts.get(e["profile_name"], 0) + 1
        all_rows = data + extra
        pos = sum(e["label"] for e in all_rows)
        metrics.update(samples=len(all_rows), positive=pos, negative=len(all_rows) - pos,
                       user_samples=len(user), auto_samples=len(sure) + len(extra), loss=round(loss, 4),
                       seconds=round(time.time() - started, 1))

        MODEL_DIR.mkdir(parents=True, exist_ok=True)
        tmp = HEAD_PATH.with_suffix(".tmp")
        now = time.time()
        torch.save({"state": net.state_dict(), "samples": len(all_rows), "metrics": metrics,
                    "user_counts": user_counts, "auto_counts": auto_counts, "trained_at": now}, tmp)
        tmp.replace(HEAD_PATH)
        with self.lock:
            self.net, self.samples, self.metrics = net, len(all_rows), metrics
            self.user_counts, self.auto_counts, self.trained_at = user_counts, auto_counts, now
        log.info("нейросеть дообучена: %s", metrics)
        return metrics

    @staticmethod
    def _fit(X: torch.Tensor, y: torch.Tensor, w: torch.Tensor) -> tuple[RelevanceNet, float]:
        torch.manual_seed(0)
        net = RelevanceNet()
        pos = float(y.sum())
        # Перекос классов компенсируем частично (корень из отношения): полная компенсация
        # делает сеть слишком щедрой на «подходит» при сотне положительных примеров на тысячи.
        ratio = (len(y) - pos) / max(pos, 1.0)
        loss_fn = nn.BCEWithLogitsLoss(reduction="none", pos_weight=torch.tensor([math.sqrt(ratio)]))
        # На больших выборках хватает меньшего числа эпох — обучение укладывается в десятки секунд.
        epochs = max(15, min(150, 150 * 300 // len(y)))
        loss_val = 0.0
        opt = torch.optim.AdamW(net.parameters(), lr=2e-3, weight_decay=1e-2)
        net.train()
        for _ in range(epochs):
            perm = torch.randperm(len(y))
            total = 0.0
            for i in range(0, len(y), 64):
                b = perm[i:i + 64]
                opt.zero_grad()
                # Сглаживание меток: авторазметка шумная, сеть не должна быть уверена на 100%.
                loss = (loss_fn(net(X[b]), y[b] * 0.9 + 0.05) * w[b]).sum() / w[b].sum()
                loss.backward()
                opt.step()
                total += float(loss) * len(b)
            loss_val = total / len(y)
        net.eval()
        return net, loss_val
