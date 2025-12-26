package disk

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpen(t *testing.T) {
	// Create a temporary file to test with
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.img")

	// Create a 1MB test file
	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	testData := make([]byte, 1024*1024) // 1MB
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	f.Write(testData)
	f.Close()

	// Test opening the file
	reader, err := Open(tmpFile)
	if err != nil {
		t.Fatalf("Failed to open test file: %v", err)
	}
	defer reader.Close()

	// Verify size
	if reader.Size() != int64(len(testData)) {
		t.Errorf("Expected size %d, got %d", len(testData), reader.Size())
	}

	// Verify sector size
	if reader.SectorSize() != SectorSize {
		t.Errorf("Expected sector size %d, got %d", SectorSize, reader.SectorSize())
	}
}

func TestReadAt(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.img")

	// Create test file with known pattern
	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	testData := []byte("Hello, World! This is a test file for disk reader.")
	f.Write(testData)
	f.Close()

	reader, err := Open(tmpFile)
	if err != nil {
		t.Fatalf("Failed to open test file: %v", err)
	}
	defer reader.Close()

	// Read at offset 0
	buf := make([]byte, 5)
	n, err := reader.ReadAt(buf, 0)
	if err != nil {
		t.Fatalf("ReadAt failed: %v", err)
	}
	if n != 5 {
		t.Errorf("Expected to read 5 bytes, got %d", n)
	}
	if string(buf) != "Hello" {
		t.Errorf("Expected 'Hello', got '%s'", string(buf))
	}

	// Read at offset 7
	n, err = reader.ReadAt(buf, 7)
	if err != nil {
		t.Fatalf("ReadAt failed: %v", err)
	}
	if string(buf) != "World" {
		t.Errorf("Expected 'World', got '%s'", string(buf))
	}
}

func TestReadSector(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.img")

	// Create a file with 2 sectors
	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	sector1 := make([]byte, SectorSize)
	sector2 := make([]byte, SectorSize)
	for i := range sector1 {
		sector1[i] = 0xAA
	}
	for i := range sector2 {
		sector2[i] = 0xBB
	}
	f.Write(sector1)
	f.Write(sector2)
	f.Close()

	reader, err := Open(tmpFile)
	if err != nil {
		t.Fatalf("Failed to open test file: %v", err)
	}
	defer reader.Close()

	// Read sector 0
	data, err := reader.ReadSector(0)
	if err != nil {
		t.Fatalf("ReadSector failed: %v", err)
	}
	if data[0] != 0xAA || data[SectorSize-1] != 0xAA {
		t.Errorf("Sector 0 data mismatch")
	}

	// Read sector 1
	data, err = reader.ReadSector(1)
	if err != nil {
		t.Fatalf("ReadSector failed: %v", err)
	}
	if data[0] != 0xBB || data[SectorSize-1] != 0xBB {
		t.Errorf("Sector 1 data mismatch")
	}
}

func TestDetectFilesystem(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected string
		wantErr  bool
	}{
		{
			name: "NTFS",
			data: func() []byte {
				buf := make([]byte, 4096)
				copy(buf[3:7], "NTFS")
				return buf
			}(),
			expected: "ntfs",
			wantErr:  false,
		},
		{
			name: "FAT32 at offset 82",
			data: func() []byte {
				buf := make([]byte, 4096)
				copy(buf[82:87], "FAT32")
				return buf
			}(),
			expected: "fat32",
			wantErr:  false,
		},
		{
			name: "FAT32 at offset 54",
			data: func() []byte {
				buf := make([]byte, 4096)
				copy(buf[54:59], "FAT32")
				return buf
			}(),
			expected: "fat32",
			wantErr:  false,
		},
		{
			name:     "Unknown",
			data:     make([]byte, 4096),
			expected: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.img")

			if err := os.WriteFile(tmpFile, tt.data, 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			reader, err := Open(tmpFile)
			if err != nil {
				t.Fatalf("Failed to open test file: %v", err)
			}
			defer reader.Close()

			fs, err := DetectFilesystem(reader)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("DetectFilesystem failed: %v", err)
			}
			if fs != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, fs)
			}
		})
	}
}
