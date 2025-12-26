package carver

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/shubham/recovery/internal/disk"
)

func TestSignatureDetection(t *testing.T) {
	tests := []struct {
		name      string
		header    []byte
		padding   int
		wantType  string
		wantCount int
	}{
		{
			name:      "JPEG",
			header:    []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46},
			wantType:  "JPEG",
			wantCount: 1,
		},
		{
			name:      "PNG",
			header:    []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
			wantType:  "PNG",
			wantCount: 1,
		},
		{
			name:      "PDF",
			header:    []byte{0x25, 0x50, 0x44, 0x46, 0x2D, 0x31, 0x2E, 0x34},
			wantType:  "PDF",
			wantCount: 1,
		},
		{
			name:      "GIF",
			header:    []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61},
			wantType:  "GIF",
			wantCount: 1,
		},
		{
			name:      "ZIP/DOCX",
			header:    []byte{0x50, 0x4B, 0x03, 0x04},
			wantType:  "DOCX", // First match
			wantCount: 4,      // DOCX, XLSX, PPTX, ZIP all match
		},
		{
			name:      "No signature",
			header:    []byte{0x00, 0x00, 0x00, 0x00},
			wantType:  "",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.img")

			// Create file with signature + padding
			data := make([]byte, 64*1024) // 64KB (smaller for tests)
			copy(data, tt.header)

			if err := os.WriteFile(tmpFile, data, 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			reader, err := disk.Open(tmpFile)
			if err != nil {
				t.Fatalf("Failed to open test file: %v", err)
			}
			defer reader.Close()

			carver := NewCarver(reader)
			files, err := carver.Scan()
			if err != nil {
				t.Fatalf("Scan failed: %v", err)
			}

			if len(files) != tt.wantCount {
				t.Errorf("Expected %d files, got %d", tt.wantCount, len(files))
			}

			if tt.wantCount > 0 && files[0].Signature.Name != tt.wantType {
				t.Errorf("Expected type %s, got %s", tt.wantType, files[0].Signature.Name)
			}
		})
	}
}

func TestMultipleSignatures(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.img")

	// Create file with multiple embedded signatures
	data := make([]byte, 64*1024) // 64KB

	// JPEG at offset 0
	copy(data[0:], []byte{0xFF, 0xD8, 0xFF, 0xE0})

	// PNG at offset 10KB
	copy(data[10*1024:], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})

	// PDF at offset 30KB
	copy(data[30*1024:], []byte{0x25, 0x50, 0x44, 0x46})

	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	reader, err := disk.Open(tmpFile)
	if err != nil {
		t.Fatalf("Failed to open test file: %v", err)
	}
	defer reader.Close()

	carver := NewCarver(reader)
	files, err := carver.Scan()
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	// Should find at least 3 files
	if len(files) < 3 {
		t.Errorf("Expected at least 3 files, got %d", len(files))
	}

	// Check offsets
	foundOffsets := make(map[int64]string)
	for _, f := range files {
		foundOffsets[f.Offset] = f.Signature.Name
	}

	expectedOffsets := map[int64]string{
		0:         "JPEG",
		10 * 1024: "PNG",
		30 * 1024: "PDF",
	}

	for offset, name := range expectedOffsets {
		if foundOffsets[offset] != name {
			t.Errorf("Expected %s at offset %d, got %s", name, offset, foundOffsets[offset])
		}
	}
}

func TestRecoverFile(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.img")
	outputDir := filepath.Join(tmpDir, "output")

	// Create a fake JPEG with header and footer
	jpegHeader := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	jpegFooter := []byte{0xFF, 0xD9}
	jpegContent := bytes.Repeat([]byte{0x42}, 1000)

	data := make([]byte, 1024*1024)
	copy(data[0:], jpegHeader)
	copy(data[len(jpegHeader):], jpegContent)
	copy(data[len(jpegHeader)+len(jpegContent):], jpegFooter)

	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	reader, err := disk.Open(tmpFile)
	if err != nil {
		t.Fatalf("Failed to open test file: %v", err)
	}
	defer reader.Close()

	carver := NewCarver(reader)
	files, err := carver.Scan()
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(files) == 0 {
		t.Fatal("No files found")
	}

	// Recover the file
	path, err := carver.RecoverFile(files[0], outputDir, 0)
	if err != nil {
		t.Fatalf("RecoverFile failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("Recovered file does not exist: %s", path)
	}

	// Verify content
	recovered, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read recovered file: %v", err)
	}

	// Should contain header
	if !bytes.HasPrefix(recovered, jpegHeader) {
		t.Errorf("Recovered file missing JPEG header")
	}

	// Should end with footer
	if !bytes.HasSuffix(recovered, jpegFooter) {
		t.Errorf("Recovered file missing JPEG footer")
	}
}

func TestSetSignatures(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.img")

	// Create file with JPEG signature
	data := make([]byte, 64*1024) // 64KB
	copy(data[0:], []byte{0xFF, 0xD8, 0xFF, 0xE0})

	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	reader, err := disk.Open(tmpFile)
	if err != nil {
		t.Fatalf("Failed to open test file: %v", err)
	}
	defer reader.Close()

	carver := NewCarver(reader)

	// Set custom signatures (only PNG)
	carver.SetSignatures([]FileSignature{
		{Name: "PNG", Extension: ".png", Header: []byte{0x89, 0x50, 0x4E, 0x47}},
	})

	files, err := carver.Scan()
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	// Should not find JPEG since we only look for PNG
	if len(files) != 0 {
		t.Errorf("Expected 0 files with PNG-only filter, got %d", len(files))
	}
}
