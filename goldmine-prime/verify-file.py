from qdrant_client import QdrantClient, models

client = QdrantClient(url="http://localhost:6333")
COLLECTION_NAME = "obsidian_vault"

# Search for any points where the 'path' payload contains our target file
results = client.scroll(
    collection_name=COLLECTION_NAME,
    scroll_filter=models.Filter(
        must=[
            models.FieldCondition(
                key="path",
                match=models.MatchValue(value="Properties/0 Hayes Run Rd. New Hill NC.md")
            )
        ]
    ),
    limit=10,
    with_payload=True
)

print(f"Found {len(results[0])} chunks for this file.\n")
for i, point in enumerate(results[0]):
    print(f"--- CHUNK {i} (ID: {point.id}) ---")
    print(point.payload.get("content"))