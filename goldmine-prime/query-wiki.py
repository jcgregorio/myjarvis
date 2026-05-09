"""Query the WIKIPEDIA_ENGLISH Qdrant collection.

Runs the same query against both sparse vectors (SPLADE 'text-sparse' and
'bm25') and prints timings and top hits for each.
"""

import sys
import time

from fastembed import SparseTextEmbedding
from qdrant_client import QdrantClient, models

COLLECTION = "WIKIPEDIA_ENGLISH"
LIMIT = 5

queries = sys.argv[1:] or [
    "who invented the transistor",
    "first programmable computer",
    "the great barrier reef ecosystem",
]

client = QdrantClient(url="http://localhost:6333")

print("Loading SPLADE...", flush=True)
splade = SparseTextEmbedding(model_name="prithivida/Splade_PP_en_v1")
print("Loading BM25...", flush=True)
bm25 = SparseTextEmbedding(model_name="Qdrant/bm25")


def to_sparse(emb):
    return models.SparseVector(
        indices=emb.indices.tolist(), values=emb.values.tolist()
    )


PREFETCH_LIMIT = 50


def print_hits(label: str, t_embed_ms: float, t_search_ms: float, points):
    print(f"\n  [{label}] embed={t_embed_ms:.0f}ms  search={t_search_ms:.0f}ms  hits={len(points)}")
    for p in points:
        title = p.payload.get("title", "?")
        url = p.payload.get("url", "")
        snippet = (p.payload.get("content") or "").replace("\n", " ")[:140]
        print(f"    {p.score:.3f}  {title}  {url}")
        print(f"           {snippet}")


def run_single(query_text: str, using: str, embedder):
    t0 = time.perf_counter()
    emb = list(embedder.embed([query_text]))[0]
    t_embed = (time.perf_counter() - t0) * 1000

    t0 = time.perf_counter()
    res = client.query_points(
        collection_name=COLLECTION,
        query=to_sparse(emb),
        using=using,
        limit=LIMIT,
        with_payload=["title", "url", "content"],
    )
    t_search = (time.perf_counter() - t0) * 1000
    print_hits(using, t_embed, t_search, res.points)


def run_hybrid(query_text: str, fusion: models.Fusion):
    t0 = time.perf_counter()
    splade_vec = to_sparse(list(splade.embed([query_text]))[0])
    bm25_vec = to_sparse(list(bm25.embed([query_text]))[0])
    t_embed = (time.perf_counter() - t0) * 1000

    t0 = time.perf_counter()
    res = client.query_points(
        collection_name=COLLECTION,
        prefetch=[
            models.Prefetch(query=splade_vec, using="text-sparse", limit=PREFETCH_LIMIT),
            models.Prefetch(query=bm25_vec, using="bm25", limit=PREFETCH_LIMIT),
        ],
        query=models.FusionQuery(fusion=fusion),
        limit=LIMIT,
        with_payload=["title", "url", "content"],
    )
    t_search = (time.perf_counter() - t0) * 1000
    print_hits(f"hybrid/{fusion.value}", t_embed, t_search, res.points)


for q in queries:
    print(f"\n=== Query: {q!r} ===")
    run_single(q, "text-sparse", splade)
    run_single(q, "bm25", bm25)
    run_hybrid(q, models.Fusion.RRF)
    run_hybrid(q, models.Fusion.DBSF)
