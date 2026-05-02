import os
import uuid
import hashlib
import logging
from qdrant_client import QdrantClient, models
from fastembed import SparseTextEmbedding

# --- Configuration ---
VAULT_PATH = "/home/jcgregorio/obsidian"
QDRANT_URL = "http://localhost:6333"
COLLECTION_NAME = "obsidian_vault"

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# Initialize Clients
client = QdrantClient(url=QDRANT_URL)
model = SparseTextEmbedding(model_name="prithivida/Splade_PP_en_v1")

def get_content_hash(text):
    """Generates a SHA-256 hash to track content changes."""
    return hashlib.sha256(text.encode('utf-8')).hexdigest()

def ensure_collection():
    """Mandatory: Define the collection and sparse vector parameters."""
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

def chunk_markdown(content, max_chunk_chars=500):
    """Splits by headers, then sub-splits by length if necessary."""
    sections = []
    lines = content.split('\n')
    current_chunk = []  # Fixed: Properly initialized
    current_heading = "Top Level"
    
    # Pass 1: Split into sections based on Markdown headers
    for line in lines:
        if line.startswith(('# ', '## ', '### ')):
            if current_chunk:
                sections.append(("\n".join(current_chunk), current_heading))
            current_heading = line.strip('# ')
            current_chunk = [line]
        else:
            current_chunk.append(line)
            
    # Don't forget the last section
    if current_chunk:
        sections.append(("\n".join(current_chunk), current_heading))

    # Pass 2: Sub-split sections that exceed the token/character limit
    final_chunks = []
    for text, heading in sections:
        if len(text) <= max_chunk_chars:
            final_chunks.append((text, heading))
        else:
            # Recursive sub-splitting to capture the "tail" of long tables
            start = 0
            while start < len(text):
                end = start + max_chunk_chars
                
                # Attempt to break at a newline to avoid splitting a row mid-pipe
                if end < len(text):
                    newline_idx = text.rfind('\n', start, end)
                    if newline_idx != -1 and newline_idx > start:
                        end = newline_idx
                
                chunk_text = text[start:end].strip()
                if chunk_text:
                    # Optional: Prepend context to sub-chunks so they stay searchable
                    context_text = f"Context: {heading}\n{chunk_text}"
                    final_chunks.append((context_text, heading))
                
                start = end
                
    return final_chunks

def index_vault():
    ensure_collection()
    all_paths = []
    for root, _, files in os.walk(VAULT_PATH):
        # Skip internal Obsidian and Git folders
        if any(x in root for x in [".git", ".obsidian", ".trash"]):
            continue
            
        for file in files:
            if not file.endswith(".md"):
                continue
                
            file_path = os.path.join(root, file)
            all_paths.append(file_path)
            rel_path = os.path.relpath(file_path, VAULT_PATH)
            
            try:
                with open(file_path, 'r', encoding='utf-8') as f:
                    content = f.read()
                
                chunks = chunk_markdown(content)
                
                for i, (text, heading) in enumerate(chunks):
                    # Stable ID based on path and chunk index
                    point_id = str(uuid.uuid5(uuid.NAMESPACE_DNS, f"{rel_path}_{i}"))
                    new_hash = get_content_hash(text)
                    
                    # Incremental Check: Fetch existing point metadata
                    existing = client.retrieve(
                        collection_name=COLLECTION_NAME,
                        ids=[point_id],
                        with_payload=True
                    )
                    
                    #if existing and existing[0].payload.get("hash") == new_hash:
                    #    continue # Skip re-embedding if content hasn't changed
                    
                    logger.info(f"Indexing: {rel_path} > {heading}")
                    
                    # Generate SPLADE Sparse Vector
                    combined_text = f"File: {rel_path} \nSection: {heading} \n\n{text}"
                    embeddings = list(model.embed([combined_text]))[0]

                    sparse_vector = models.SparseVector(
                        indices=embeddings.indices.tolist(),
                        values=embeddings.values.tolist()
                    )

                    # Upsert to Qdrant
                    client.upsert(
                        collection_name=COLLECTION_NAME,
                        points=[
                            models.PointStruct(
                                id=point_id,
                                vector={"text-sparse": sparse_vector},
                                payload={
                                    "content": text,
                                    "hash": new_hash,
                                    "path": rel_path,
                                    "heading": heading,
                                    "source": "obsidian"
                                }
                            )
                        ]
                    )
                    logger.info(f"File {file_path}")
            except Exception as e:
                logger.error(f"Error processing {file_path}: {e}")
    logger.info(f"Paths {all_paths}")

if __name__ == "__main__":
    index_vault()
    logger.info("Indexing complete.")