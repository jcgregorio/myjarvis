import os
import uuid
import logging
from datasets import load_dataset
from qdrant_client import QdrantClient, models
from fastembed import SparseTextEmbedding

# --- Configuration ---
SNAPSHOT = "20231101.en"
COLLECTION_NAME = "wiki_en"
CHECKPOINT_FILE = "/mnt/archive/wiki_checkpoint.txt"
BATCH_SIZE = 50

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# Initialize
client = QdrantClient(url="http://localhost:6333")
model = SparseTextEmbedding(model_name="prithivida/Splade_PP_en_v1")

def get_last_checkpoint():
    if os.path.exists(CHECKPOINT_FILE):
        with open(CHECKPOINT_FILE, "r") as f:
            return int(f.read().strip())
    return 0

def save_checkpoint(index):
    with open(CHECKPOINT_FILE, "w") as f:
        f.write(str(index))

def ensure_collection():
    if not client.collection_exists(COLLECTION_NAME):
        logger.info(f"Creating collection: {COLLECTION_NAME}")
        client.create_collection(
            collection_name=COLLECTION_NAME,
            sparse_vectors_config={
                "text-sparse": models.SparseVectorParams(
                    index=models.SparseIndexParams(on_disk=True)
                )
            }
        )

def chunk_wikipedia(title, text, max_chars=500):
    """Chunks text while injecting the article title for context."""
    chunks = []
    start = 0
    while start < len(text):
        end = start + max_chars
        if end < len(text):
            newline_idx = text.rfind('\n', start, end)
            if newline_idx != -1 and newline_idx > start:
                end = newline_idx
        
        chunk_body = text[start:end].strip()
        if chunk_body:
            # Context Injection: Title + Chunk
            full_text = f"Wikipedia: {title}\n\n{chunk_body}"
            chunks.append(full_text)
        start = end
    return chunks

def run_ingestion():
    ensure_collection()
    last_idx = get_last_checkpoint()
    
    logger.info(f"Starting Wikipedia ingestion from index {last_idx}...")
    ds = load_dataset("wikimedia/wikipedia", SNAPSHOT, split="train", streaming=True)
    
    batch_points = []
    
    for i, article in enumerate(ds):
        # Skip until we hit our checkpoint
        if i < last_idx:
            continue

        title = article['title']
        text = article['text']
        
        chunks = chunk_wikipedia(title, text)
        
        for chunk_idx, chunk_text in enumerate(chunks):
            # Stable ID for reproducibility
            point_id = str(uuid.uuid5(uuid.NAMESPACE_DNS, f"wiki_{title}_{chunk_idx}"))
            
            # SPLADE Embedding
            embeddings = list(model.embed([chunk_text]))[0]
            sparse_vector = models.SparseVector(
                indices=embeddings.indices.tolist(),
                values=embeddings.values.tolist()
            )
            
            batch_points.append(models.PointStruct(
                id=point_id,
                vector={"text-sparse": sparse_vector},
                payload={"title": title, "content": chunk_text}
            ))

        # Upsert in batches for speed
        if len(batch_points) >= BATCH_SIZE:
            client.upsert(collection_name=COLLECTION_NAME, points=batch_points)
            batch_points = []
            
            if i % 100 == 0:
                logger.info(f"Indexed {i} articles. Current: {title}")
                save_checkpoint(i)

if __name__ == "__main__":
    run_ingestion()