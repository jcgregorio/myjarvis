from qdrant_client import QdrantClient, models
from fastembed import SparseTextEmbedding

# Use the same model as the indexer
model = SparseTextEmbedding(model_name="prithivida/Splade_PP_en_v1")
client = QdrantClient(url="http://localhost:6333")

query_text = "property" # Or any keyword in your notes

# 1. Embed the query
# model.embed returns a generator of SparseEmbedding objects
embeddings = list(model.embed([query_text]))[0]

# 2. Convert to Qdrant's SparseVector format
# This extracts the raw indices and values into the format Qdrant understands
query_vector = models.SparseVector(
    indices=embeddings.indices.tolist(),
    values=embeddings.values.tolist()
)

# 3. Search
# We pass the SparseVector object to the 'query' parameter
results = client.query_points(
    collection_name="obsidian_vault",
    query=query_vector,
    using="text-sparse",
    limit=3
)

# 4. Print Results
print(f"\n--- Results for: '{query_text}' ---")
if not results.points:
    print("No matches found.")
else:
    for res in results.points:
        print(f"\n[Score: {res.score:.4f}] | Path: {res.payload.get('path')}")
        print(f"Heading: {res.payload.get('heading')}")
        print("-" * 30)
        # Handle cases where content might be missing
        content = res.payload.get('content', "No content found.")
        print(content[:200] + "...")