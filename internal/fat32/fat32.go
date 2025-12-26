package fat32

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/shubham/recovery/internal/disk"
)

const (
	DirEntrySize     = 32
	DeletedMarker    = 0xE5
	LFNAttribute     = 0x0F
	AttrDirectory    = 0x10
	AttrVolumeLabel  = 0x08
	ClusterEndMarker = 0x0FFFFFF8
)

// BootSector represents FAT32 boot sector
type BootSector struct {
	JumpBoot          [3]byte
	OEMName           [8]byte
	BytesPerSector    uint16
	SectorsPerCluster uint8
	ReservedSectors   uint16
	NumFATs           uint8
	RootEntryCount    uint16 // 0 for FAT32
	TotalSectors16    uint16
	Media             uint8
	FATSize16         uint16 // 0 for FAT32
	SectorsPerTrack   uint16
	NumHeads          uint16
	HiddenSectors     uint32
	TotalSectors32    uint32
	// FAT32 specific
	FATSize32        uint32
	ExtFlags         uint16
	FSVersion        uint16
	RootCluster      uint32
	FSInfo           uint16
	BackupBootSector uint16
	Reserved         [12]byte
	DriveNumber      uint8
	Reserved1        uint8
	BootSig          uint8
	VolumeID         uint32
	VolumeLabel      [11]byte
	FSType           [8]byte
}

// DirectoryEntry represents a FAT32 directory entry
type DirectoryEntry struct {
	Name          [11]byte
	Attr          uint8
	NTRes         uint8
	CrtTimeTenth  uint8
	CrtTime       uint16
	CrtDate       uint16
	LstAccDate    uint16
	FirstClusterH uint16
	WrtTime       uint16
	WrtDate       uint16
	FirstClusterL uint16
	FileSize      uint32
}

// LFNEntry represents a Long File Name entry
type LFNEntry struct {
	Ordinal   uint8
	Name1     [10]byte // 5 UTF-16 chars
	Attr      uint8
	Type      uint8
	Checksum  uint8
	Name2     [12]byte // 6 UTF-16 chars
	FirstClus uint16
	Name3     [4]byte // 2 UTF-16 chars
}

// RecoveredFile holds info about a deleted file
type RecoveredFile struct {
	Name         string
	LongName     string
	Path         string
	FirstCluster uint32
	Size         uint32
	IsDirectory  bool
	IsDeleted    bool
}

// FAT32 parser
type Parser struct {
	reader     *disk.Reader
	bootSector *BootSector
	fatStart   int64
	dataStart  int64
	clusterSz  int
	fatTable   []uint32
}

func NewParser(reader *disk.Reader) (*Parser, error) {
	p := &Parser{reader: reader}

	if err := p.readBootSector(); err != nil {
		return nil, err
	}

	return p, nil
}

func (p *Parser) readBootSector() error {
	buf := make([]byte, 512)
	if _, err := p.reader.ReadAt(buf, 0); err != nil {
		return fmt.Errorf("failed to read boot sector: %w", err)
	}

	p.bootSector = &BootSector{}
	p.bootSector.BytesPerSector = binary.LittleEndian.Uint16(buf[11:13])
	p.bootSector.SectorsPerCluster = buf[13]
	p.bootSector.ReservedSectors = binary.LittleEndian.Uint16(buf[14:16])
	p.bootSector.NumFATs = buf[16]
	p.bootSector.TotalSectors32 = binary.LittleEndian.Uint32(buf[32:36])
	p.bootSector.FATSize32 = binary.LittleEndian.Uint32(buf[36:40])
	p.bootSector.RootCluster = binary.LittleEndian.Uint32(buf[44:48])

	// Calculate offsets
	p.fatStart = int64(p.bootSector.ReservedSectors) * int64(p.bootSector.BytesPerSector)
	fatSize := int64(p.bootSector.FATSize32) * int64(p.bootSector.BytesPerSector)
	p.dataStart = p.fatStart + int64(p.bootSector.NumFATs)*fatSize
	p.clusterSz = int(p.bootSector.SectorsPerCluster) * int(p.bootSector.BytesPerSector)

	return nil
}

func (p *Parser) loadFAT() error {
	fatSize := int(p.bootSector.FATSize32) * int(p.bootSector.BytesPerSector)
	buf := make([]byte, fatSize)

	if _, err := p.reader.ReadAt(buf, p.fatStart); err != nil {
		return fmt.Errorf("failed to read FAT: %w", err)
	}

	p.fatTable = make([]uint32, fatSize/4)
	for i := range p.fatTable {
		p.fatTable[i] = binary.LittleEndian.Uint32(buf[i*4:])
	}

	return nil
}

func (p *Parser) clusterToOffset(cluster uint32) int64 {
	return p.dataStart + int64(cluster-2)*int64(p.clusterSz)
}

func (p *Parser) readCluster(cluster uint32) ([]byte, error) {
	offset := p.clusterToOffset(cluster)
	buf := make([]byte, p.clusterSz)
	if _, err := p.reader.ReadAt(buf, offset); err != nil {
		return nil, err
	}
	return buf, nil
}

// ScanDeletedFiles scans directory entries for deleted files
func (p *Parser) ScanDeletedFiles() ([]RecoveredFile, error) {
	if err := p.loadFAT(); err != nil {
		return nil, err
	}

	var files []RecoveredFile
	visited := make(map[uint32]bool)

	// Start from root cluster
	if err := p.scanDirectory(p.bootSector.RootCluster, "", &files, visited); err != nil {
		return nil, err
	}

	return files, nil
}

func (p *Parser) scanDirectory(cluster uint32, path string, files *[]RecoveredFile, visited map[uint32]bool) error {
	for cluster != 0 && cluster < ClusterEndMarker {
		if visited[cluster] {
			break
		}
		visited[cluster] = true

		data, err := p.readCluster(cluster)
		if err != nil {
			return err
		}

		var lfnParts []string

		for i := 0; i < len(data); i += DirEntrySize {
			entry := data[i : i+DirEntrySize]

			if entry[0] == 0x00 {
				// End of directory
				break
			}

			// Check for LFN entry
			if entry[11] == LFNAttribute {
				lfn := p.parseLFNEntry(entry)
				if entry[0]&0x40 != 0 {
					lfnParts = nil // First LFN entry
				}
				lfnParts = append([]string{lfn}, lfnParts...)
				continue
			}

			// Skip volume labels
			if entry[11]&AttrVolumeLabel != 0 {
				continue
			}

			isDeleted := entry[0] == DeletedMarker
			isDir := entry[11]&AttrDirectory != 0

			firstCluster := uint32(binary.LittleEndian.Uint16(entry[26:28])) |
				(uint32(binary.LittleEndian.Uint16(entry[20:22])) << 16)
			fileSize := binary.LittleEndian.Uint32(entry[28:32])

			// Build name
			shortName := p.parseShortName(entry[:11], isDeleted)
			longName := strings.Join(lfnParts, "")
			lfnParts = nil

			name := longName
			if name == "" {
				name = shortName
			}

			if name == "." || name == ".." {
				continue
			}

			file := RecoveredFile{
				Name:         shortName,
				LongName:     longName,
				Path:         filepath.Join(path, name),
				FirstCluster: firstCluster,
				Size:         fileSize,
				IsDirectory:  isDir,
				IsDeleted:    isDeleted,
			}

			if isDeleted {
				*files = append(*files, file)
			}

			// Recurse into directories (but not deleted ones - clusters may be reused)
			if isDir && !isDeleted && firstCluster >= 2 {
				if err := p.scanDirectory(firstCluster, file.Path, files, visited); err != nil {
					// Continue on error
				}
			}
		}

		// Follow cluster chain
		if int(cluster) < len(p.fatTable) {
			cluster = p.fatTable[cluster]
		} else {
			break
		}
	}

	return nil
}

func (p *Parser) parseLFNEntry(entry []byte) string {
	var chars []uint16

	// Name1: 5 chars at offset 1
	for j := 0; j < 5; j++ {
		c := binary.LittleEndian.Uint16(entry[1+j*2:])
		if c == 0 || c == 0xFFFF {
			break
		}
		chars = append(chars, c)
	}

	// Name2: 6 chars at offset 14
	for j := 0; j < 6; j++ {
		c := binary.LittleEndian.Uint16(entry[14+j*2:])
		if c == 0 || c == 0xFFFF {
			break
		}
		chars = append(chars, c)
	}

	// Name3: 2 chars at offset 28
	for j := 0; j < 2; j++ {
		c := binary.LittleEndian.Uint16(entry[28+j*2:])
		if c == 0 || c == 0xFFFF {
			break
		}
		chars = append(chars, c)
	}

	return string(utf16.Decode(chars))
}

func (p *Parser) parseShortName(name []byte, isDeleted bool) string {
	baseName := strings.TrimRight(string(name[:8]), " ")
	ext := strings.TrimRight(string(name[8:11]), " ")

	if isDeleted && len(baseName) > 0 {
		// First byte was 0xE5, try to guess original char (usually unknown)
		baseName = "?" + baseName[1:]
	}

	if ext != "" {
		return baseName + "." + ext
	}
	return baseName
}

// RecoverFile extracts a deleted file's data
func (p *Parser) RecoverFile(file RecoveredFile, outputPath string) error {
	if file.IsDirectory {
		return os.MkdirAll(outputPath, 0755)
	}

	// For deleted files, we can only recover the first cluster chain
	// since FAT entries are zeroed. We estimate clusters needed.
	clustersNeeded := (file.Size + uint32(p.clusterSz) - 1) / uint32(p.clusterSz)
	if clustersNeeded == 0 {
		clustersNeeded = 1
	}

	// Create output directory
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	var bytesWritten uint32
	cluster := file.FirstCluster

	for i := uint32(0); i < clustersNeeded && bytesWritten < file.Size; i++ {
		data, err := p.readCluster(cluster)
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		toWrite := uint32(len(data))
		remaining := file.Size - bytesWritten
		if toWrite > remaining {
			toWrite = remaining
		}

		if _, err := outFile.Write(data[:toWrite]); err != nil {
			return err
		}

		bytesWritten += toWrite

		// For deleted files, assume contiguous clusters
		cluster++
	}

	return nil
}

// Recover is the main entry point for FAT32 recovery
func Recover(reader *disk.Reader, outputDir string, scanOnly bool, carveMode bool) (int, error) {
	parser, err := NewParser(reader)
	if err != nil {
		return 0, err
	}

	fmt.Printf("FAT32 filesystem detected\n")
	fmt.Printf("  Bytes per sector: %d\n", parser.bootSector.BytesPerSector)
	fmt.Printf("  Sectors per cluster: %d\n", parser.bootSector.SectorsPerCluster)
	fmt.Printf("  Cluster size: %d bytes\n", parser.clusterSz)
	fmt.Printf("  Root cluster: %d\n", parser.bootSector.RootCluster)
	fmt.Println()

	files, err := parser.ScanDeletedFiles()
	if err != nil {
		return 0, err
	}

	fmt.Printf("Found %d deleted files:\n\n", len(files))
	for i, f := range files {
		name := f.LongName
		if name == "" {
			name = f.Name
		}
		fileType := "FILE"
		if f.IsDirectory {
			fileType = "DIR "
		}
		fmt.Printf("[%d] %s %s (%d bytes)\n", i+1, fileType, f.Path, f.Size)
	}

	if scanOnly {
		return len(files), nil
	}

	fmt.Println("\nRecovering files...")
	recovered := 0
	for _, f := range files {
		if f.IsDirectory {
			continue
		}

		name := f.LongName
		if name == "" {
			name = f.Name
		}
		outPath := filepath.Join(outputDir, f.Path)

		if err := parser.RecoverFile(f, outPath); err != nil {
			fmt.Printf("  Failed to recover %s: %v\n", name, err)
			continue
		}
		fmt.Printf("  Recovered: %s\n", outPath)
		recovered++
	}

	return recovered, nil
}
