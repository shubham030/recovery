package ntfs

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/shubham/recovery/internal/disk"
)

func createNTFSImage(t *testing.T) string {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "ntfs.img")

	// Create a minimal NTFS boot sector
	bootSector := make([]byte, 512)

	// Jump instruction
	bootSector[0] = 0xEB
	bootSector[1] = 0x52
	bootSector[2] = 0x90

	// NTFS signature
	copy(bootSector[3:11], "NTFS    ")

	// Bytes per sector (512)
	binary.LittleEndian.PutUint16(bootSector[11:13], 512)

	// Sectors per cluster (8)
	bootSector[13] = 8

	// Reserved sectors (0 for NTFS)
	binary.LittleEndian.PutUint16(bootSector[14:16], 0)

	// Media descriptor
	bootSector[21] = 0xF8

	// Sectors per track
	binary.LittleEndian.PutUint16(bootSector[24:26], 63)

	// Number of heads
	binary.LittleEndian.PutUint16(bootSector[26:28], 255)

	// Total sectors
	binary.LittleEndian.PutUint64(bootSector[40:48], 2097152)

	// MFT cluster number
	binary.LittleEndian.PutUint64(bootSector[48:56], 100)

	// MFT mirror cluster
	binary.LittleEndian.PutUint64(bootSector[56:64], 1000)

	// Clusters per MFT record (negative means 2^|value| bytes)
	bootSector[64] = 0xF6 // -10 means 1024 bytes

	// Clusters per index record
	bootSector[68] = 0xF6

	// Boot signature
	bootSector[510] = 0x55
	bootSector[511] = 0xAA

	// Create image file
	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create NTFS image: %v", err)
	}
	defer f.Close()

	// Write boot sector
	f.Write(bootSector)

	// Pad to include MFT area
	padding := make([]byte, 10*1024*1024) // 10MB
	f.Write(padding)

	return tmpFile
}

func TestNTFSNewParser(t *testing.T) {
	imgPath := createNTFSImage(t)

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

	if parser.bootSector.MFTCluster != 100 {
		t.Errorf("Expected MFT cluster 100, got %d", parser.bootSector.MFTCluster)
	}

	// Cluster size should be 512 * 8 = 4096
	if parser.clusterSize != 4096 {
		t.Errorf("Expected cluster size 4096, got %d", parser.clusterSize)
	}

	// MFT record size should be 1024 (2^10 from -10)
	if parser.mftRecSize != 1024 {
		t.Errorf("Expected MFT record size 1024, got %d", parser.mftRecSize)
	}
}

func TestDecodeUTF16(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected string
	}{
		{
			name:     "Simple ASCII",
			input:    []byte{'H', 0, 'e', 0, 'l', 0, 'l', 0, 'o', 0},
			expected: "Hello",
		},
		{
			name:     "Empty",
			input:    []byte{},
			expected: "",
		},
		{
			name:     "Single char",
			input:    []byte{'A', 0},
			expected: "A",
		},
		{
			name:     "Filename with extension",
			input:    []byte{'t', 0, 'e', 0, 's', 0, 't', 0, '.', 0, 't', 0, 'x', 0, 't', 0},
			expected: "test.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := decodeUTF16(tt.input)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestParseDataRuns(t *testing.T) {
	p := &Parser{}

	// Test data runs parsing
	// Format: header byte (high nibble = offset bytes, low nibble = length bytes)
	// followed by length bytes, then offset bytes

	tests := []struct {
		name     string
		attr     []byte
		expected []DataRun
	}{
		{
			name: "Single run",
			attr: func() []byte {
				attr := make([]byte, 64)
				// Data runs offset at byte 32
				binary.LittleEndian.PutUint16(attr[32:34], 40)
				// At offset 40: single run
				attr[40] = 0x11         // 1 byte length, 1 byte offset
				attr[41] = 0x10         // 16 clusters
				attr[42] = 0x64         // offset 100
				attr[43] = 0x00         // end marker
				return attr
			}(),
			expected: []DataRun{{Offset: 100, Length: 16}},
		},
		{
			name: "Empty",
			attr: func() []byte {
				attr := make([]byte, 64)
				binary.LittleEndian.PutUint16(attr[32:34], 40)
				attr[40] = 0x00 // end marker
				return attr
			}(),
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.parseDataRuns(tt.attr)
			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d runs, got %d", len(tt.expected), len(result))
				return
			}
			for i, run := range result {
				if run.Offset != tt.expected[i].Offset {
					t.Errorf("Run %d: expected offset %d, got %d", i, tt.expected[i].Offset, run.Offset)
				}
				if run.Length != tt.expected[i].Length {
					t.Errorf("Run %d: expected length %d, got %d", i, tt.expected[i].Length, run.Length)
				}
			}
		})
	}
}

func TestReconstructPath(t *testing.T) {
	p := &Parser{
		mftRecords: map[uint64]*RecoveredFile{
			5:  {Name: "", MFTIndex: 5, ParentRef: 5},              // Root
			10: {Name: "Documents", MFTIndex: 10, ParentRef: 5},    // Documents folder
			20: {Name: "Work", MFTIndex: 20, ParentRef: 10},        // Work subfolder
			30: {Name: "report.pdf", MFTIndex: 30, ParentRef: 20},  // File in Work
		},
	}

	tests := []struct {
		mftIndex uint64
		expected string
	}{
		{30, "Documents/Work/report.pdf"},
		{20, "Documents/Work"},
		{10, "Documents"},
	}

	for _, tt := range tests {
		result := p.reconstructPath(tt.mftIndex)
		if result != tt.expected {
			t.Errorf("MFT %d: expected '%s', got '%s'", tt.mftIndex, tt.expected, result)
		}
	}
}

func TestMinFunc(t *testing.T) {
	tests := []struct {
		a, b     uint64
		expected uint64
	}{
		{10, 20, 10},
		{20, 10, 10},
		{10, 10, 10},
		{0, 100, 0},
	}

	for _, tt := range tests {
		result := min(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("min(%d, %d): expected %d, got %d", tt.a, tt.b, tt.expected, result)
		}
	}
}
