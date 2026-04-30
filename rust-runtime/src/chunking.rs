use base64::engine::general_purpose::STANDARD as BASE64;
use base64::Engine;

use crate::protocol::{Chunk, ChunkingConfig};

pub fn chunk_output(output: &str, config: &ChunkingConfig) -> Vec<Chunk> {
    if !config.enabled {
        return vec![Chunk {
            seq: 1,
            eof_flag: true,
            data_b64: BASE64.encode(output.as_bytes()),
        }];
    }

    let bytes = output.as_bytes();
    let mut capped = bytes;
    if config.max_total_bytes > 0 && bytes.len() > config.max_total_bytes {
        capped = &bytes[..config.max_total_bytes];
    }

    if capped.is_empty() {
        return vec![];
    }

    let step = if config.max_chunk_bytes == 0 {
        capped.len()
    } else {
        config.max_chunk_bytes
    };
    let mut chunks = Vec::new();
    let mut seq = 1usize;
    let mut idx = 0usize;

    while idx < capped.len() {
        let end = (idx + step).min(capped.len());
        let part = &capped[idx..end];
        let eof_flag = end >= capped.len();
        chunks.push(Chunk {
            seq,
            eof_flag,
            data_b64: BASE64.encode(part),
        });
        seq += 1;
        idx = end;
    }

    chunks
}

#[cfg(test)]
mod tests {
    use super::*;

    fn config(enabled: bool, max_chunk: usize, max_total: usize) -> ChunkingConfig {
        ChunkingConfig {
            enabled,
            max_chunk_bytes: max_chunk,
            max_total_bytes: max_total,
        }
    }

    #[test]
    fn test_disabled_returns_single_chunk() {
        let chunks = chunk_output("hello", &config(false, 0, 0));
        assert_eq!(chunks.len(), 1);
        assert!(chunks[0].eof_flag);
    }

    #[test]
    fn test_empty_output_returns_empty() {
        let chunks = chunk_output("", &config(true, 100, 1000));
        assert!(chunks.is_empty());
    }

    #[test]
    fn test_small_output_single_chunk() {
        let chunks = chunk_output("hello", &config(true, 100, 1000));
        assert_eq!(chunks.len(), 1);
        assert!(chunks[0].eof_flag);
        assert_eq!(chunks[0].seq, 1);
    }

    #[test]
    fn test_large_output_multiple_chunks() {
        let data = "a".repeat(1000);
        let chunks = chunk_output(&data, &config(true, 300, 0));
        assert_eq!(chunks.len(), 4); // 300+300+300+100
        assert!(!chunks[0].eof_flag);
        assert!(!chunks[1].eof_flag);
        assert!(!chunks[2].eof_flag);
        assert!(chunks[3].eof_flag);
        assert_eq!(chunks[0].seq, 1);
        assert_eq!(chunks[3].seq, 4);
    }

    #[test]
    fn test_total_bytes_limit() {
        let data = "a".repeat(2000);
        let chunks = chunk_output(&data, &config(true, 500, 1000));
        // Capped at 1000 bytes, then chunked at 500 bytes = 2 chunks
        assert_eq!(chunks.len(), 2);
        assert!(chunks[1].eof_flag);
    }
}
