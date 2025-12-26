package carver

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/shubham/recovery/internal/disk"
)

// FileSignature defines a file type's magic bytes
type FileSignature struct {
	Name      string
	Extension string
	Header    []byte
	Footer    []byte    // Optional footer for better detection
	MaxSize   int64     // Max file size to carve (0 = use default)
	Offset    int       // Offset where header appears (usually 0)
}

// Common file signatures
var Signatures = []FileSignature{
	// Images
	{Name: "JPEG", Extension: ".jpg", Header: []byte{0xFF, 0xD8, 0xFF}, Footer: []byte{0xFF, 0xD9}, MaxSize: 50 * 1024 * 1024},
	{Name: "PNG", Extension: ".png", Header: []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, Footer: []byte{0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82}, MaxSize: 50 * 1024 * 1024},
	{Name: "GIF", Extension: ".gif", Header: []byte{0x47, 0x49, 0x46, 0x38}, Footer: []byte{0x00, 0x3B}, MaxSize: 20 * 1024 * 1024},
	{Name: "BMP", Extension: ".bmp", Header: []byte{0x42, 0x4D}, MaxSize: 50 * 1024 * 1024},
	{Name: "WEBP", Extension: ".webp", Header: []byte{0x52, 0x49, 0x46, 0x46}, MaxSize: 50 * 1024 * 1024}, // RIFF header
	{Name: "TIFF", Extension: ".tiff", Header: []byte{0x49, 0x49, 0x2A, 0x00}, MaxSize: 100 * 1024 * 1024},
	{Name: "TIFF-BE", Extension: ".tiff", Header: []byte{0x4D, 0x4D, 0x00, 0x2A}, MaxSize: 100 * 1024 * 1024},

	// Videos
	{Name: "MP4", Extension: ".mp4", Header: []byte{0x00, 0x00, 0x00}, MaxSize: 4 * 1024 * 1024 * 1024}, // ftyp follows at offset 4
	{Name: "AVI", Extension: ".avi", Header: []byte{0x52, 0x49, 0x46, 0x46}, MaxSize: 4 * 1024 * 1024 * 1024},
	{Name: "MKV", Extension: ".mkv", Header: []byte{0x1A, 0x45, 0xDF, 0xA3}, MaxSize: 4 * 1024 * 1024 * 1024},
	{Name: "MOV", Extension: ".mov", Header: []byte{0x00, 0x00, 0x00, 0x14, 0x66, 0x74, 0x79, 0x70}, MaxSize: 4 * 1024 * 1024 * 1024},
	{Name: "WMV", Extension: ".wmv", Header: []byte{0x30, 0x26, 0xB2, 0x75, 0x8E, 0x66, 0xCF, 0x11}, MaxSize: 4 * 1024 * 1024 * 1024},
	{Name: "FLV", Extension: ".flv", Header: []byte{0x46, 0x4C, 0x56, 0x01}, MaxSize: 2 * 1024 * 1024 * 1024},

	// Audio
	{Name: "MP3", Extension: ".mp3", Header: []byte{0xFF, 0xFB}, MaxSize: 100 * 1024 * 1024},
	{Name: "MP3-ID3", Extension: ".mp3", Header: []byte{0x49, 0x44, 0x33}, MaxSize: 100 * 1024 * 1024},
	{Name: "WAV", Extension: ".wav", Header: []byte{0x52, 0x49, 0x46, 0x46}, MaxSize: 500 * 1024 * 1024},
	{Name: "FLAC", Extension: ".flac", Header: []byte{0x66, 0x4C, 0x61, 0x43}, MaxSize: 500 * 1024 * 1024},
	{Name: "OGG", Extension: ".ogg", Header: []byte{0x4F, 0x67, 0x67, 0x53}, MaxSize: 200 * 1024 * 1024},
	{Name: "M4A", Extension: ".m4a", Header: []byte{0x00, 0x00, 0x00, 0x20, 0x66, 0x74, 0x79, 0x70, 0x4D, 0x34, 0x41}, MaxSize: 500 * 1024 * 1024},

	// Documents
	{Name: "PDF", Extension: ".pdf", Header: []byte{0x25, 0x50, 0x44, 0x46}, Footer: []byte{0x25, 0x25, 0x45, 0x4F, 0x46}, MaxSize: 500 * 1024 * 1024},
	{Name: "DOCX", Extension: ".docx", Header: []byte{0x50, 0x4B, 0x03, 0x04}, MaxSize: 100 * 1024 * 1024},
	{Name: "XLSX", Extension: ".xlsx", Header: []byte{0x50, 0x4B, 0x03, 0x04}, MaxSize: 100 * 1024 * 1024},
	{Name: "PPTX", Extension: ".pptx", Header: []byte{0x50, 0x4B, 0x03, 0x04}, MaxSize: 500 * 1024 * 1024},
	{Name: "ZIP", Extension: ".zip", Header: []byte{0x50, 0x4B, 0x03, 0x04}, MaxSize: 1024 * 1024 * 1024},
	{Name: "RAR", Extension: ".rar", Header: []byte{0x52, 0x61, 0x72, 0x21, 0x1A, 0x07}, MaxSize: 1024 * 1024 * 1024},
	{Name: "7Z", Extension: ".7z", Header: []byte{0x37, 0x7A, 0xBC, 0xAF, 0x27, 0x1C}, MaxSize: 1024 * 1024 * 1024},

	// Executables
	{Name: "EXE", Extension: ".exe", Header: []byte{0x4D, 0x5A}, MaxSize: 500 * 1024 * 1024},
	{Name: "ELF", Extension: ".elf", Header: []byte{0x7F, 0x45, 0x4C, 0x46}, MaxSize: 500 * 1024 * 1024},

	// Database
	{Name: "SQLite", Extension: ".sqlite", Header: []byte{0x53, 0x51, 0x4C, 0x69, 0x74, 0x65, 0x20, 0x66, 0x6F, 0x72, 0x6D, 0x61, 0x74}, MaxSize: 1024 * 1024 * 1024},
}

// CarvedFile represents a recovered file
type CarvedFile struct {
	Signature *FileSignature
	Offset    int64
	Size      int64
	Path      string
}

// Carver handles file carving
type Carver struct {
	reader     *disk.Reader
	bufSize    int
	signatures []FileSignature
}

func NewCarver(reader *disk.Reader) *Carver {
	return &Carver{
		reader:     reader,
		bufSize:    1024 * 1024, // 1MB buffer
		signatures: Signatures,
	}
}

// SetSignatures allows custom signature filtering
func (c *Carver) SetSignatures(sigs []FileSignature) {
	c.signatures = sigs
}

// Scan searches for file signatures
func (c *Carver) Scan() ([]CarvedFile, error) {
	var files []CarvedFile

	diskSize := c.reader.Size()
	bufSize := c.bufSize
	if diskSize < int64(bufSize) {
		bufSize = int(diskSize)
	}
	if bufSize < 128 {
		bufSize = 128
	}
	buf := make([]byte, bufSize)
	overlap := 1024 // Overlap to catch headers at boundaries
	if overlap > bufSize/2 {
		overlap = 0
	}

	fmt.Printf("Scanning disk for file signatures (%d bytes)...\n", diskSize)

	var offset int64
	for offset < diskSize {
		n, err := c.reader.ReadAt(buf, offset)
		if err != nil && err != io.EOF {
			return nil, err
		}
		if n == 0 {
			break
		}

		// Search for signatures in buffer
		searchEnd := n - 64
		if searchEnd < 0 {
			searchEnd = n
		}
		for i := 0; i < searchEnd; i++ {
			for _, sig := range c.signatures {
				if len(sig.Header) > n-i {
					continue
				}

				if bytes.Equal(buf[i:i+len(sig.Header)], sig.Header) {
					// Additional MP4/MOV validation
					if sig.Name == "MP4" && i+8 < n {
						ftyp := string(buf[i+4 : i+8])
						if ftyp != "ftyp" {
							continue
						}
					}

					fileOffset := offset + int64(i)
					files = append(files, CarvedFile{
						Signature: &sig,
						Offset:    fileOffset,
						Size:      sig.MaxSize,
					})
				}
			}
		}

		// Progress (only for large scans)
		if diskSize > 10*1024*1024 && offset%(100*1024*1024) == 0 {
			pct := float64(offset) / float64(diskSize) * 100
			fmt.Printf("  %.1f%% scanned, found %d files...\n", pct, len(files))
		}

		// Move to next chunk, ensuring we always advance
		advance := n - overlap
		if advance <= 0 {
			advance = n
		}
		offset += int64(advance)
	}

	return files, nil
}

// RecoverFile extracts a carved file
func (c *Carver) RecoverFile(file CarvedFile, outputDir string, index int) (string, error) {
	filename := fmt.Sprintf("carved_%06d%s", index, file.Signature.Extension)
	outputPath := filepath.Join(outputDir, file.Signature.Name, filename)

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return "", err
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		return "", err
	}
	defer outFile.Close()

	maxSize := file.Signature.MaxSize
	if maxSize == 0 {
		maxSize = 10 * 1024 * 1024 // 10MB default
	}

	buf := make([]byte, 64*1024) // 64KB chunks
	var written int64
	offset := file.Offset

	for written < maxSize {
		toRead := min(int64(len(buf)), maxSize-written)
		n, err := c.reader.ReadAt(buf[:toRead], offset)
		if err != nil && err != io.EOF {
			break
		}
		if n == 0 {
			break
		}

		// Look for footer if defined
		if len(file.Signature.Footer) > 0 {
			if idx := bytes.Index(buf[:n], file.Signature.Footer); idx >= 0 {
				// Found footer, write up to and including footer
				outFile.Write(buf[:idx+len(file.Signature.Footer)])
				written += int64(idx + len(file.Signature.Footer))
				break
			}
		}

		outFile.Write(buf[:n])
		written += int64(n)
		offset += int64(n)
	}

	return outputPath, nil
}

// Recover is the main carving entry point
func Recover(reader *disk.Reader, outputDir string, scanOnly bool) (int, error) {
	carver := NewCarver(reader)

	files, err := carver.Scan()
	if err != nil {
		return 0, err
	}

	// Group by type
	byType := make(map[string]int)
	for _, f := range files {
		byType[f.Signature.Name]++
	}

	fmt.Printf("\nFound %d potential files:\n", len(files))
	for name, count := range byType {
		fmt.Printf("  %s: %d\n", name, count)
	}

	if scanOnly {
		return len(files), nil
	}

	fmt.Println("\nRecovering files...")
	recovered := 0
	for i, f := range files {
		path, err := carver.RecoverFile(f, outputDir, i)
		if err != nil {
			fmt.Printf("  Failed to recover file at offset %d: %v\n", f.Offset, err)
			continue
		}
		fmt.Printf("  Recovered: %s\n", path)
		recovered++
	}

	return recovered, nil
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
