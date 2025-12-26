package fat32

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/shubham/recovery/internal/disk"
)

func createFAT32Image(t *testing.T) string {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "fat32.img")

	// Create a minimal FAT32 boot sector
	bootSector := make([]byte, 512)

	// Jump instruction
	bootSector[0] = 0xEB
	bootSector[1] = 0x58
	bootSector[2] = 0x90

	// OEM Name
	copy(bootSector[3:11], "MSDOS5.0")

	// Bytes per sector (512)
	binary.LittleEndian.PutUint16(bootSector[11:13], 512)

	// Sectors per cluster (8)
	bootSector[13] = 8

	// Reserved sectors (32)
	binary.LittleEndian.PutUint16(bootSector[14:16], 32)

	// Number of FATs (2)
	bootSector[16] = 2

	// Root entry count (0 for FAT32)
	binary.LittleEndian.PutUint16(bootSector[17:19], 0)

	// Total sectors 16 (0 for FAT32)
	binary.LittleEndian.PutUint16(bootSector[19:21], 0)

	// Media descriptor
	bootSector[21] = 0xF8

	// FAT size 16 (0 for FAT32)
	binary.LittleEndian.PutUint16(bootSector[22:24], 0)

	// Sectors per track
	binary.LittleEndian.PutUint16(bootSector[24:26], 63)

	// Number of heads
	binary.LittleEndian.PutUint16(bootSector[26:28], 255)

	// Hidden sectors
	binary.LittleEndian.PutUint32(bootSector[28:32], 0)

	// Total sectors 32
	binary.LittleEndian.PutUint32(bootSector[32:36], 2097152) // 1GB

	// FAT32 specific: FAT size 32
	binary.LittleEndian.PutUint32(bootSector[36:40], 2048)

	// Root cluster
	binary.LittleEndian.PutUint32(bootSector[44:48], 2)

	// FSInfo sector
	binary.LittleEndian.PutUint16(bootSector[48:50], 1)

	// Backup boot sector
	binary.LittleEndian.PutUint16(bootSector[50:52], 6)

	// File system type
	copy(bootSector[82:90], "FAT32   ")

	// Boot signature
	bootSector[510] = 0x55
	bootSector[511] = 0xAA

	// Create image file
	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create FAT32 image: %v", err)
	}
	defer f.Close()

	// Write boot sector
	f.Write(bootSector)

	// Pad to include FAT tables and data region
	padding := make([]byte, 10*1024*1024) // 10MB
	f.Write(padding)

	return tmpFile
}

func TestNewParser(t *testing.T) {
	imgPath := createFAT32Image(t)

	reader, err := disk.Open(imgPath)
	if err != nil {
		t.Fatalf("Failed to open image: %v", err)
	}
	defer reader.Close()

	parser, err := NewParser(reader)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}

	// Verify boot sector was parsed correctly
	if parser.bootSector.BytesPerSector != 512 {
		t.Errorf("Expected 512 bytes per sector, got %d", parser.bootSector.BytesPerSector)
	}

	if parser.bootSector.SectorsPerCluster != 8 {
		t.Errorf("Expected 8 sectors per cluster, got %d", parser.bootSector.SectorsPerCluster)
	}

	if parser.bootSector.RootCluster != 2 {
		t.Errorf("Expected root cluster 2, got %d", parser.bootSector.RootCluster)
	}

	if parser.clusterSz != 512*8 {
		t.Errorf("Expected cluster size %d, got %d", 512*8, parser.clusterSz)
	}
}

func TestParseShortName(t *testing.T) {
	p := &Parser{}

	tests := []struct {
		name      string
		input     []byte
		isDeleted bool
		expected  string
	}{
		{
			name:      "Simple name",
			input:     []byte{'T', 'E', 'S', 'T', ' ', ' ', ' ', ' ', 'T', 'X', 'T'},
			isDeleted: false,
			expected:  "TEST.TXT",
		},
		{
			name:      "No extension",
			input:     []byte{'F', 'O', 'L', 'D', 'E', 'R', ' ', ' ', ' ', ' ', ' '},
			isDeleted: false,
			expected:  "FOLDER",
		},
		{
			name:      "Deleted file",
			input:     []byte{0xE5, 'E', 'S', 'T', ' ', ' ', ' ', ' ', 'T', 'X', 'T'},
			isDeleted: true,
			expected:  "?EST.TXT",
		},
		{
			name:      "Full name",
			input:     []byte{'M', 'Y', 'F', 'I', 'L', 'E', '~', '1', 'D', 'O', 'C'},
			isDeleted: false,
			expected:  "MYFILE~1.DOC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.parseShortName(tt.input, tt.isDeleted)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestParseLFNEntry(t *testing.T) {
	p := &Parser{}

	// Create a fake LFN entry for "Hello"
	// LFN stores name in UTF-16LE
	entry := make([]byte, 32)
	entry[0] = 0x41 // First (and last) LFN entry
	entry[11] = 0x0F // LFN attribute

	// Name1 (5 chars at offset 1): "Hello"
	entry[1] = 'H'
	entry[2] = 0
	entry[3] = 'e'
	entry[4] = 0
	entry[5] = 'l'
	entry[6] = 0
	entry[7] = 'l'
	entry[8] = 0
	entry[9] = 'o'
	entry[10] = 0

	// Name2 (6 chars at offset 14): terminator
	entry[14] = 0
	entry[15] = 0

	result := p.parseLFNEntry(entry)
	if result != "Hello" {
		t.Errorf("Expected 'Hello', got '%s'", result)
	}
}

func TestClusterToOffset(t *testing.T) {
	p := &Parser{
		dataStart: 1024 * 1024, // 1MB
		clusterSz: 4096,        // 4KB clusters
	}

	tests := []struct {
		cluster  uint32
		expected int64
	}{
		{2, 1024 * 1024},             // First data cluster
		{3, 1024*1024 + 4096},        // Second data cluster
		{10, 1024*1024 + 8*4096},     // Cluster 10
	}

	for _, tt := range tests {
		result := p.clusterToOffset(tt.cluster)
		if result != tt.expected {
			t.Errorf("Cluster %d: expected offset %d, got %d", tt.cluster, tt.expected, result)
		}
	}
}
