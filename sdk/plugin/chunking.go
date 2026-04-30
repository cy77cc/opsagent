package plugin

import "encoding/base64"

// ChunkSize is the default maximum payload size (256 KiB) before chunking kicks in.
const ChunkSize = 256 * 1024

// ChunkOutput splits a large output string into base64-encoded chunks.
// Returns nil if the output fits within a single chunk of maxChunkBytes.
// If maxChunkBytes <= 0, ChunkSize is used as the default.
func ChunkOutput(output string, maxChunkBytes int) []Chunk {
	if maxChunkBytes <= 0 {
		maxChunkBytes = ChunkSize
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(output))
	if len(encoded) <= maxChunkBytes {
		return nil
	}

	var chunks []Chunk
	seq := 0
	for len(encoded) > 0 {
		end := maxChunkBytes
		if end > len(encoded) {
			end = len(encoded)
		}

		chunks = append(chunks, Chunk{
			Seq:     seq,
			EOF:     end == len(encoded),
			DataB64: encoded[:end],
		})

		encoded = encoded[end:]
		seq++
	}

	return chunks
}
