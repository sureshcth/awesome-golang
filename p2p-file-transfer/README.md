# P2P File Transfer System in Go

A peer-to-peer file transfer system implemented in Go, showcasing advanced networking concepts and the libp2p networking stack.

## Features

- Decentralized peer-to-peer architecture
- Content-addressed file storage (files identified by hash)
- Chunked file transfers for efficient downloading
- Parallel chunk downloading with retry mechanisms
- Automatic peer discovery
- File verification using SHA-256 hashing
- Simple command-line interface

## Advanced Concepts Demonstrated

1. **Content-addressed storage** - Files are identified by their SHA-256 hash rather than location
2. **Peer-to-peer networking** using the libp2p library
3. **Concurrent programming** with goroutines and synchronization primitives
4. **Protocol design** with custom message formats
5. **Chunk-based file transfer** with parallel downloads