package p2pfiletransfer

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
)

const (
	protocolID         = "/p2p-file-transfer/1.0.0"
	maxMessageSize     = 1 << 20 // 1MB
	initialBufferSize  = 1024
	connectionTimeout  = 5 * time.Second
	transferTimeout    = 30 * time.Minute
	discoveryInterval  = 10 * time.Second
	fileChunkSize      = 1024 * 64 // 64KB chunks
	announcementPrefix = "ANNOUNCE:"
	requestPrefix      = "REQUEST:"
	chunkPrefix        = "CHUNK:"
)

// FileInfo holds metadata about a file
type FileInfo struct {
	Name      string
	Size      int64
	Hash      string
	ChunkSize int
	NumChunks int
}

// FileRegistry maintains a list of files available locally and remotely
type FileRegistry struct {
	sync.RWMutex
	LocalFiles  map[string]FileInfo
	RemoteFiles map[string]map[peer.ID]FileInfo // Hash -> PeerID -> FileInfo
}

// FileTransfer manages the file transfer state
type FileTransfer struct {
	sync.Mutex
	ActiveTransfers map[string]*TransferState
}

// TransferState tracks the progress of a file transfer
type TransferState struct {
	FileInfo    FileInfo
	Destination string
	ReceivedMap []bool
	Progress    int
	StartTime   time.Time
}

// P2PNode encapsulates all the functionality for the P2P file transfer
type P2PNode struct {
	Host        host.Host
	Registry    *FileRegistry
	Transfers   *FileTransfer
	ShareDir    string
	DownloadDir string
	Discovery   *PeerDiscovery
	ctx         context.Context
	cancel      context.CancelFunc
}

// PeerDiscovery handles peer discovery functionality
type PeerDiscovery struct {
	announceInterval time.Duration
	discoveredPeers  map[peer.ID]time.Time
	peerLock         sync.RWMutex
	node             *P2PNode
}

// NewFileRegistry creates a new file registry
func NewFileRegistry() *FileRegistry {
	return &FileRegistry{
		LocalFiles:  make(map[string]FileInfo),
		RemoteFiles: make(map[string]map[peer.ID]FileInfo),
	}
}

// NewFileTransfer creates a new file transfer manager
func NewFileTransfer() *FileTransfer {
	return &FileTransfer{
		ActiveTransfers: make(map[string]*TransferState),
	}
}

// NewPeerDiscovery creates a new peer discovery manager
func NewPeerDiscovery(node *P2PNode, interval time.Duration) *PeerDiscovery {
	return &PeerDiscovery{
		announceInterval: interval,
		discoveredPeers:  make(map[peer.ID]time.Time),
		node:             node,
	}
}

// NewP2PNode creates a new P2P node
func NewP2PNode(shareDir, downloadDir string) (*P2PNode, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create a new libp2p host
	h, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"),
		libp2p.EnableRelay(),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create host: %v", err)
	}

	// Ensure directories exist
	if err := os.MkdirAll(shareDir, 0755); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create share directory: %v", err)
	}
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create download directory: %v", err)
	}

	node := &P2PNode{
		Host:        h,
		Registry:    NewFileRegistry(),
		Transfers:   NewFileTransfer(),
		ShareDir:    shareDir,
		DownloadDir: downloadDir,
		ctx:         ctx,
		cancel:      cancel,
	}

	// Create peer discovery
	node.Discovery = NewPeerDiscovery(node, discoveryInterval)

	return node, nil
}

// Start begins the P2P node operation
func (n *P2PNode) Start() error {
	// Register protocol handler
	n.Host.SetStreamHandler(protocol.ID(protocolID), n.handleStream)

	// Scan and register local files
	if err := n.scanLocalFiles(); err != nil {
		return fmt.Errorf("failed to scan local files: %v", err)
	}

	// Start peer discovery
	go n.Discovery.run()

	// Print node address
	log.Printf("P2P Node started. Your address: %s/p2p/%s\n",
		n.Host.Addrs()[0], n.Host.ID())

	return nil
}

// Stop shuts down the P2P node
func (n *P2PNode) Stop() {
	n.cancel()
	if err := n.Host.Close(); err != nil {
		log.Printf("Error closing host: %v", err)
	}
}

// Connect establishes a connection to a peer
func (n *P2PNode) Connect(addr string) error {
	maddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return fmt.Errorf("invalid address: %v", err)
	}

	peerInfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return fmt.Errorf("failed to get peer info: %v", err)
	}

	ctx, cancel := context.WithTimeout(n.ctx, connectionTimeout)
	defer cancel()

	if err := n.Host.Connect(ctx, *peerInfo); err != nil {
		return fmt.Errorf("failed to connect: %v", err)
	}

	// Add peer to discovered peers
	n.Discovery.addPeer(peerInfo.ID)

	log.Printf("Connected to peer: %s\n", peerInfo.ID)
	return nil
}

// handleStream processes incoming streams
func (n *P2PNode) handleStream(s network.Stream) {
	defer s.Close()

	reader := bufio.NewReader(s)
	message, err := reader.ReadString('\n')
	if err != nil {
		log.Printf("Error reading message: %v", err)
		return
	}

	message = strings.TrimSpace(message)

	// Process message based on prefix
	switch {
	case strings.HasPrefix(message, announcementPrefix):
		n.handleAnnouncement(s.Conn().RemotePeer(), message[len(announcementPrefix):])
	case strings.HasPrefix(message, requestPrefix):
		n.handleRequest(s, message[len(requestPrefix):])
	default:
		log.Printf("Unknown message format: %s", message)
	}
}

// handleAnnouncement processes file announcements from peers
func (n *P2PNode) handleAnnouncement(peerID peer.ID, message string) {
	// Parse file info from announcement
	parts := strings.Split(message, "|")
	if len(parts) < 4 {
		log.Printf("Invalid announcement format")
		return
	}

	fileInfo := FileInfo{
		Name:      parts[0],
		Hash:      parts[1],
		ChunkSize: fileChunkSize,
	}

	// Parse size
	fmt.Sscanf(parts[2], "%d", &fileInfo.Size)
	fmt.Sscanf(parts[3], "%d", &fileInfo.NumChunks)

	// Update registry
	n.Registry.Lock()
	defer n.Registry.Unlock()

	if _, exists := n.Registry.RemoteFiles[fileInfo.Hash]; !exists {
		n.Registry.RemoteFiles[fileInfo.Hash] = make(map[peer.ID]FileInfo)
	}

	n.Registry.RemoteFiles[fileInfo.Hash][peerID] = fileInfo
	log.Printf("Received file announcement from %s: %s (%s)",
		peerID, fileInfo.Name, fileInfo.Hash)
}

// handleRequest processes file chunk requests
func (n *P2PNode) handleRequest(s network.Stream, message string) {
	parts := strings.Split(message, "|")
	if len(parts) < 2 {
		log.Printf("Invalid request format")
		return
	}

	fileHash := parts[0]
	chunkIndex := 0
	fmt.Sscanf(parts[1], "%d", &chunkIndex)

	// Locate file in registry
	n.Registry.RLock()
	fileInfo, exists := n.Registry.LocalFiles[fileHash]
	n.Registry.RUnlock()

	if !exists {
		log.Printf("Requested file not found: %s", fileHash)
		return
	}

	// Open file
	filePath := filepath.Join(n.ShareDir, fileInfo.Name)
	file, err := os.Open(filePath)
	if err != nil {
		log.Printf("Error opening file: %v", err)
		return
	}
	defer file.Close()

	// Seek to chunk position
	offset := int64(chunkIndex) * int64(fileInfo.ChunkSize)
	_, err = file.Seek(offset, io.SeekStart)
	if err != nil {
		log.Printf("Error seeking in file: %v", err)
		return
	}

	// Read chunk
	chunkSize := fileInfo.ChunkSize
	if offset+int64(chunkSize) > fileInfo.Size {
		chunkSize = int(fileInfo.Size - offset)
	}

	chunk := make([]byte, chunkSize)
	bytesRead, err := file.Read(chunk)
	if err != nil && err != io.EOF {
		log.Printf("Error reading chunk: %v", err)
		return
	}

	// Send chunk
	writer := bufio.NewWriter(s)
	response := fmt.Sprintf("%s%s|%d|%d\n", chunkPrefix, fileHash, chunkIndex, bytesRead)
	if _, err := writer.WriteString(response); err != nil {
		log.Printf("Error writing response header: %v", err)
		return
	}

	if _, err := writer.Write(chunk[:bytesRead]); err != nil {
		log.Printf("Error writing chunk data: %v", err)
		return
	}

	if err := writer.Flush(); err != nil {
		log.Printf("Error flushing writer: %v", err)
		return
	}

	log.Printf("Sent chunk %d of file %s", chunkIndex, fileInfo.Name)
}

// scanLocalFiles scans the share directory and registers files
func (n *P2PNode) scanLocalFiles() error {
	files, err := os.ReadDir(n.ShareDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		fileInfo, err := file.Info()
		if err != nil {
			log.Fatal(err)
		}
		if fileInfo.IsDir() {
			continue
		}

		filePath := filepath.Join(n.ShareDir, fileInfo.Name())
		hash, err := calculateFileHash(filePath)
		if err != nil {
			log.Printf("Error calculating hash for %s: %v", filePath, err)
			continue
		}

		numChunks := (fileInfo.Size() + int64(fileChunkSize) - 1) / int64(fileChunkSize)

		fi := FileInfo{
			Name:      fileInfo.Name(),
			Size:      fileInfo.Size(),
			Hash:      hash,
			ChunkSize: fileChunkSize,
			NumChunks: int(numChunks),
		}

		n.Registry.Lock()
		n.Registry.LocalFiles[hash] = fi
		n.Registry.Unlock()

		log.Printf("Registered local file: %s (%s)", fileInfo.Name(), hash)
	}

	return nil
}

// ListLocalFiles returns a list of local files
func (n *P2PNode) ListLocalFiles() []FileInfo {
	n.Registry.RLock()
	defer n.Registry.RUnlock()

	files := make([]FileInfo, 0, len(n.Registry.LocalFiles))
	for _, file := range n.Registry.LocalFiles {
		files = append(files, file)
	}

	return files
}

// ListRemoteFiles returns a list of remote files
func (n *P2PNode) ListRemoteFiles() map[string][]peer.ID {
	n.Registry.RLock()
	defer n.Registry.RUnlock()

	files := make(map[string][]peer.ID)
	for _, peers := range n.Registry.RemoteFiles {
		for peerID, fileInfo := range peers {
			if _, exists := files[fileInfo.Name]; !exists {
				files[fileInfo.Name] = make([]peer.ID, 0)
			}
			files[fileInfo.Name] = append(files[fileInfo.Name], peerID)
		}
	}

	return files
}

// DownloadFile initiates a file download
func (n *P2PNode) DownloadFile(hash string, peerID peer.ID) error {
	// Check if file is already being downloaded
	n.Transfers.Lock()
	if _, exists := n.Transfers.ActiveTransfers[hash]; exists {
		n.Transfers.Unlock()
		return fmt.Errorf("file is already being downloaded")
	}
	n.Transfers.Unlock()

	// Get file info
	n.Registry.RLock()
	fileMap, exists := n.Registry.RemoteFiles[hash]
	if !exists {
		n.Registry.RUnlock()
		return fmt.Errorf("file not found in registry")
	}

	fileInfo, exists := fileMap[peerID]
	if !exists {
		n.Registry.RUnlock()
		return fmt.Errorf("file not available from specified peer")
	}
	n.Registry.RUnlock()

	// Create transfer state
	destination := filepath.Join(n.DownloadDir, fileInfo.Name)
	receivedMap := make([]bool, fileInfo.NumChunks)

	transferState := &TransferState{
		FileInfo:    fileInfo,
		Destination: destination,
		ReceivedMap: receivedMap,
		StartTime:   time.Now(),
	}

	// Add to active transfers
	n.Transfers.Lock()
	n.Transfers.ActiveTransfers[hash] = transferState
	n.Transfers.Unlock()

	// Start download
	go n.downloadFileChunks(hash, peerID, transferState)

	return nil
}

// downloadFileChunks downloads file chunks in parallel
func (n *P2PNode) downloadFileChunks(hash string, peerID peer.ID, state *TransferState) {
	// Create temporary file
	tempPath := state.Destination + ".part"
	file, err := os.Create(tempPath)
	if err != nil {
		log.Printf("Error creating temp file: %v", err)
		return
	}
	defer file.Close()

	// Expand file to full size
	if err := file.Truncate(state.FileInfo.Size); err != nil {
		log.Printf("Error resizing file: %v", err)
		return
	}

	var wg sync.WaitGroup
	// Use buffered channel as semaphore for concurrent downloads
	semaphore := make(chan struct{}, 5) // Max 5 concurrent chunk downloads

	ctx, cancel := context.WithTimeout(n.ctx, transferTimeout)
	defer cancel()

	// Download all chunks
	for i := 0; i < state.FileInfo.NumChunks; i++ {
		wg.Add(1)
		chunkIndex := i

		go func() {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire
			defer func() { <-semaphore }() // Release

			for attempts := 0; attempts < 3; attempts++ {
				if err := n.downloadChunk(ctx, hash, chunkIndex, peerID, file, state); err != nil {
					log.Printf("Error downloading chunk %d (attempt %d): %v",
						chunkIndex, attempts+1, err)
					time.Sleep(1 * time.Second)
					continue
				}
				return
			}
		}()
	}

	wg.Wait()

	// Check if all chunks were downloaded
	complete := true
	for _, received := range state.ReceivedMap {
		if !received {
			complete = false
			break
		}
	}

	if complete {
		// Rename temporary file to final destination
		if err := os.Rename(tempPath, state.Destination); err != nil {
			log.Printf("Error renaming file: %v", err)
			return
		}

		// Calculate downloaded file hash for verification
		downloadedHash, err := calculateFileHash(state.Destination)
		if err != nil {
			log.Printf("Error calculating hash for downloaded file: %v", err)
			return
		}

		if downloadedHash != hash {
			log.Printf("Downloaded file hash mismatch: expected %s, got %s", hash, downloadedHash)
			os.Remove(state.Destination)
			return
		}

		// Register downloaded file
		n.Registry.Lock()
		n.Registry.LocalFiles[hash] = state.FileInfo
		n.Registry.Unlock()

		log.Printf("Download complete: %s", state.FileInfo.Name)
	} else {
		log.Printf("Download incomplete: %s", state.FileInfo.Name)
	}

	// Remove from active transfers
	n.Transfers.Lock()
	delete(n.Transfers.ActiveTransfers, hash)
	n.Transfers.Unlock()
}

// downloadChunk downloads a single file chunk
func (n *P2PNode) downloadChunk(ctx context.Context, hash string, index int, peerID peer.ID,
	file *os.File, state *TransferState) error {

	// Open stream to peer
	stream, err := n.Host.NewStream(ctx, peerID, protocol.ID(protocolID))
	if err != nil {
		return fmt.Errorf("failed to open stream: %v", err)
	}
	defer stream.Close()

	// Send chunk request
	request := fmt.Sprintf("%s%s|%d\n", requestPrefix, hash, index)
	if _, err := stream.Write([]byte(request)); err != nil {
		return fmt.Errorf("failed to send request: %v", err)
	}

	// Read response
	reader := bufio.NewReader(stream)
	response, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read response: %v", err)
	}

	response = strings.TrimSpace(response)
	if !strings.HasPrefix(response, chunkPrefix) {
		return fmt.Errorf("invalid response prefix: %s", response)
	}

	// Parse chunk metadata
	parts := strings.Split(response[len(chunkPrefix):], "|")
	if len(parts) < 3 {
		return fmt.Errorf("invalid chunk metadata format")
	}

	respHash := parts[0]
	if respHash != hash {
		return fmt.Errorf("hash mismatch: expected %s, got %s", hash, respHash)
	}

	chunkIndex := 0
	chunkSize := 0
	fmt.Sscanf(parts[1], "%d", &chunkIndex)
	fmt.Sscanf(parts[2], "%d", &chunkSize)

	if chunkIndex != index {
		return fmt.Errorf("chunk index mismatch: expected %d, got %d", index, chunkIndex)
	}

	// Read chunk data
	chunk := make([]byte, chunkSize)
	bytesRead, err := io.ReadFull(reader, chunk)
	if err != nil {
		return fmt.Errorf("failed to read chunk data: %v", err)
	}

	if bytesRead != chunkSize {
		return fmt.Errorf("incomplete chunk data: expected %d bytes, got %d", chunkSize, bytesRead)
	}

	// Write chunk to file
	offset := int64(index) * int64(state.FileInfo.ChunkSize)
	if _, err := file.WriteAt(chunk, offset); err != nil {
		return fmt.Errorf("failed to write chunk: %v", err)
	}

	// Update transfer state
	n.Transfers.Lock()
	state.ReceivedMap[index] = true
	state.Progress++
	progress := float64(state.Progress) / float64(state.FileInfo.NumChunks) * 100
	n.Transfers.Unlock()

	log.Printf("Downloaded chunk %d/%d (%.1f%%) of %s",
		index+1, state.FileInfo.NumChunks, progress, state.FileInfo.Name)

	return nil
}

// announceFiles announces available files to connected peers
func (n *P2PNode) announceFiles() {
	n.Registry.RLock()
	files := make([]FileInfo, 0, len(n.Registry.LocalFiles))
	for _, file := range n.Registry.LocalFiles {
		files = append(files, file)
	}
	n.Registry.RUnlock()

	if len(files) == 0 {
		return
	}

	// Get connected peers
	for _, peerID := range n.Host.Network().Peers() {
		if peerID == n.Host.ID() {
			continue
		}

		// Open stream to peer
		stream, err := n.Host.NewStream(n.ctx, peerID, protocol.ID(protocolID))
		if err != nil {
			log.Printf("Failed to open stream to %s: %v", peerID, err)
			continue
		}

		// Announce all files
		for _, file := range files {
			announcement := fmt.Sprintf("%s%s|%s|%d|%d\n",
				announcementPrefix, file.Name, file.Hash, file.Size, file.NumChunks)
			if _, err := stream.Write([]byte(announcement)); err != nil {
				log.Printf("Failed to send announcement to %s: %v", peerID, err)
				break
			}
		}

		stream.Close()
	}
}

// run starts the peer discovery process
func (p *PeerDiscovery) run() {
	ticker := time.NewTicker(p.announceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.node.ctx.Done():
			return
		case <-ticker.C:
			// Announce files to peers
			p.node.announceFiles()

			// Prune old peers
			p.prunePeers()
		}
	}
}

// addPeer adds a peer to the discovered peers list
func (p *PeerDiscovery) addPeer(id peer.ID) {
	p.peerLock.Lock()
	defer p.peerLock.Unlock()

	p.discoveredPeers[id] = time.Now()
}

// prunePeers removes peers that haven't been seen recently
func (p *PeerDiscovery) prunePeers() {
	p.peerLock.Lock()
	defer p.peerLock.Unlock()

	now := time.Now()
	for id, lastSeen := range p.discoveredPeers {
		if now.Sub(lastSeen) > 5*p.announceInterval {
			delete(p.discoveredPeers, id)
		}
	}
}

// calculateFileHash computes the SHA-256 hash of a file
func calculateFileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func main() {
	// Parse command line arguments
	shareDir := flag.String("share", "./shared", "Directory to share files from")
	downloadDir := flag.String("download", "./downloads", "Directory to download files to")
	connectAddr := flag.String("connect", "", "Address of peer to connect to")
	flag.Parse()

	// Create P2P node
	node, err := NewP2PNode(*shareDir, *downloadDir)
	if err != nil {
		log.Fatalf("Failed to create P2P node: %v", err)
	}
	defer node.Stop()

	// Start node
	if err := node.Start(); err != nil {
		log.Fatalf("Failed to start P2P node: %v", err)
	}

	// Connect to peer if specified
	if *connectAddr != "" {
		go func() {
			time.Sleep(1 * time.Second) // Wait for node to start
			if err := node.Connect(*connectAddr); err != nil {
				log.Printf("Failed to connect to peer: %v", err)
			}
		}()
	}

	// Simple CLI for interaction
	fmt.Println("P2P File Transfer System")
	fmt.Println("Type 'help' for available commands")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		input := scanner.Text()
		parts := strings.Fields(input)
		if len(parts) == 0 {
			continue
		}

		switch parts[0] {
		case "help":
			fmt.Println("Available commands:")
			fmt.Println("  help                      - Show this help")
			fmt.Println("  connect <address>         - Connect to peer")
			fmt.Println("  list local                - List local files")
			fmt.Println("  list remote               - List remote files")
			fmt.Println("  download <hash> <peer-id> - Download file")
			fmt.Println("  quit                      - Exit program")

		case "connect":
			if len(parts) < 2 {
				fmt.Println("Usage: connect <address>")
				continue
			}
			if err := node.Connect(parts[1]); err != nil {
				fmt.Printf("Error: %v\n", err)
			}

		case "list":
			if len(parts) < 2 {
				fmt.Println("Usage: list [local|remote]")
				continue
			}

			if parts[1] == "local" {
				files := node.ListLocalFiles()
				fmt.Printf("Local files (%d):\n", len(files))
				for _, file := range files {
					fmt.Printf("  %s (%s) - %d bytes\n", file.Name, file.Hash, file.Size)
				}
			} else if parts[1] == "remote" {
				files := node.ListRemoteFiles()
				fmt.Printf("Remote files (%d):\n", len(files))
				for name, peers := range files {
					fmt.Printf("  %s - available from %d peers\n", name, len(peers))
				}
			} else {
				fmt.Println("Unknown list type. Use 'local' or 'remote'")
			}

		case "download":
			if len(parts) < 3 {
				fmt.Println("Usage: download <hash> <peer-id>")
				continue
			}

			hash := parts[1]
			peerIDStr := parts[2]

			peerID, err := peer.Decode(peerIDStr)
			if err != nil {
				fmt.Printf("Invalid peer ID: %v\n", err)
				continue
			}

			if err := node.DownloadFile(hash, peerID); err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Println("Download started")
			}

		case "quit", "exit":
			return

		default:
			fmt.Println("Unknown command. Type 'help' for available commands")
		}
	}
}
