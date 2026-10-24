"""ML-сервис: оценка релевантности заказов (ключевые слова + собственная самообучающаяся нейросеть на PyTorch)."""
import logging
import os
import threading
import time

import torch
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

from .scorer import SELF_TRAIN_INTERVAL, Scorer

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")

torch.set_num_threads(int(os.getenv("TORCH_THREADS") or 2))

app = FastAPI(title="sobachka-ml")
scorer = Scorer()
log = logging.getLogger("main")


def self_train_loop():
    # Сеть дообучается сама: раз в интервал, если появились новые заказы.
    while True:
        time.sleep(SELF_TRAIN_INTERVAL)
        try:
            scorer.maybe_self_train()
        except Exception:
            log.exception("самообучение упало")


threading.Thread(target=self_train_loop, daemon=True, name="self-train").start()


class Order(BaseModel):
    title: str = ""
    description: str = ""
    category: str = ""


class Profile(BaseModel):
    id: int = 0
    name: str = ""
    keywords: list[str] | None = None


class ScoreRequest(BaseModel):
    order: Order
    profiles: list[Profile]


class TrainRequest(BaseModel):
    examples: list[dict]
    unlabeled: list[Order] | None = None
    profiles: list[Profile] | None = None


@app.get("/health")
def health():
    return {"status": "ok", "head": scorer.info()}


@app.post("/score")
def score(req: ScoreRequest):
    return {"scores": scorer.score(req.order.model_dump(), [p.model_dump() for p in req.profiles])}


@app.post("/train")
def train(req: TrainRequest):
    try:
        return scorer.train(
            req.examples,
            [o.model_dump() for o in req.unlabeled or []],
            None if req.profiles is None else [p.model_dump() for p in req.profiles],
        )
    except ValueError as e:
        raise HTTPException(400, str(e)) from e
