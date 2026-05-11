"""HTTP RAG search server for myjarvis.

Exposes /search which runs a hybrid SPLADE+BM25 query against a Qdrant
collection (DBSF fusion) and returns the top-k hits with score + payload.

Run:
  .venv/bin/uvicorn search-server:app --host 0.0.0.0 --port 8011

Or via the systemd user unit (scripts/myjarvis-search.service).
"""

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field
from fastembed import SparseTextEmbedding
from qdrant_client import QdrantClient, models

QDRANT_URL = "http://localhost:6333"

app = FastAPI(title="myjarvis search")
client = QdrantClient(url=QDRANT_URL)
splade = SparseTextEmbedding(model_name="prithivida/Splade_PP_en_v1")
bm25 = SparseTextEmbedding(model_name="Qdrant/bm25")


def to_sparse(emb):
    return models.SparseVector(
        indices=emb.indices.tolist(),
        values=emb.values.tolist(),
    )


class SearchRequest(BaseModel):
    collection: str
    query: str
    limit: int = Field(default=5, ge=1, le=50)
    prefetch: int = Field(default=50, ge=1, le=500)


class Hit(BaseModel):
    score: float
    payload: dict


@app.get("/healthz")
def healthz():
    return {"ok": True}


@app.post("/search", response_model=list[Hit])
def search(req: SearchRequest):
    try:
        splade_vec = to_sparse(list(splade.query_embed([req.query]))[0])
        bm25_vec = to_sparse(list(bm25.query_embed([req.query]))[0])
        res = client.query_points(
            collection_name=req.collection,
            prefetch=[
                models.Prefetch(query=splade_vec, using="text-sparse", limit=req.prefetch),
                models.Prefetch(query=bm25_vec, using="bm25", limit=req.prefetch),
            ],
            query=models.FusionQuery(fusion=models.Fusion.DBSF),
            limit=req.limit,
            with_payload=True,
        )
        return [Hit(score=p.score, payload=p.payload or {}) for p in res.points]
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))
