package ntfs

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
	MFTRecordSize       = 1024
	MFTRecordMagic      = "FILE"
	AttrStandardInfo    = 0x10
	AttrFileName        = 0x30
	AttrData            = 0x80
	AttrIndexRoot       = 0x90
	AttrIndexAllocation = 0xA0
	AttrEnd             = 0xFFFFFFFF
)

// BootSector represents NTFS boot sector
type BootSector struct {
	Jump                [3]byte
	OEMName             [8]byte
	BytesPerSector      uint16
	SectorsPerCluster   uint8
	ReservedSectors     uint16
	_                   [3]byte
	_                   uint16
	MediaDescriptor     uint8
	_                   uint16
	SectorsPerTrack     uint16
	NumHeads            uint16
	HiddenSectors       uint32
	_                   uint32
	_                   uint32
	TotalSectors        uint64
	MFTCluster          uint64
	MFTMirrCluster      uint64
	ClustersPerMFTRec   int8
	_                   [3]byte
	ClustersPerIndexRec int8
}

// MFTRecord represents an MFT entry
type MFTRecord struct {
	Magic             [4]byte
	UpdateSeqOffset   uint16
	UpdateSeqSize     uint16
	LogSeqNum         uint64
	SeqNum            uint16
	LinkCount         uint16
	AttrsOffset       uint16
	Flags             uint16
	UsedSize          uint32
	AllocSize         uint32
	BaseRecRef        uint64
	NextAttrID        uint16
}

// AttributeHeader is the common attribute header
type AttributeHeader struct {
	Type       uint32
	Length     uint32
	NonResident uint8
	NameLength uint8
	NameOffset uint16
	Flags      uint16
	AttrID     uint16
}

// ResidentAttr holds resident attribute data
type ResidentAttr struct {
	ValueLength uint32
	ValueOffset uint16
	Flags       uint16
}

// NonResidentAttr holds non-resident attribute info
type NonResidentAttr struct {
	StartVCN        uint64
	EndVCN          uint64
	DataRunsOffset  uint16
	CompressionUnit uint16
	_               uint32
	AllocSize       uint64
	RealSize        uint64
	InitSize        uint64
}

// FileNameAttr represents $FILE_NAME attribute
type FileNameAttr struct {
	ParentRef   uint64
	CreateTime  uint64
	ModifyTime  uint64
	MFTModTime  uint64
	AccessTime  uint64
	AllocSize   uint64
	RealSize    uint64
	Flags       uint32
	Reparse     uint32
	NameLength  uint8
	NameType    uint8
	// Name follows (UTF-16LE)
}

// RecoveredFile holds info about a deleted file
type RecoveredFile struct {
	Name         string
	Path         string
	MFTIndex     uint64
	ParentRef    uint64
	Size         uint64
	IsDirectory  bool
	IsDeleted    bool
	DataRuns     []DataRun
}

// DataRun represents a cluster run
type DataRun struct {
	Offset int64  // Cluster offset (can be negative for sparse)
	Length uint64 // Number of clusters
}

// Parser handles NTFS parsing
type Parser struct {
	reader       *disk.Reader
	bootSector   *BootSector
	mftStart     int64
	clusterSize  int
	mftRecSize   int
	mftRecords   map[uint64]*RecoveredFile
}

func NewParser(reader *disk.Reader) (*Parser, error) {
	p := &Parser{
		reader:     reader,
		mftRecords: make(map[uint64]*RecoveredFile),
	}

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

	// Verify NTFS signature
	if string(buf[3:7]) != "NTFS" {
		return fmt.Errorf("not an NTFS filesystem")
	}

	p.bootSector = &BootSector{}
	p.bootSector.BytesPerSector = binary.LittleEndian.Uint16(buf[11:13])
	p.bootSector.SectorsPerCluster = buf[13]
	p.bootSector.MFTCluster = binary.LittleEndian.Uint64(buf[48:56])
	p.bootSector.ClustersPerMFTRec = int8(buf[64])

	p.clusterSize = int(p.bootSector.SectorsPerCluster) * int(p.bootSector.BytesPerSector)

	// Calculate MFT record size
	if p.bootSector.ClustersPerMFTRec < 0 {
		p.mftRecSize = 1 << uint(-p.bootSector.ClustersPerMFTRec)
	} else {
		p.mftRecSize = int(p.bootSector.ClustersPerMFTRec) * p.clusterSize
	}

	p.mftStart = int64(p.bootSector.MFTCluster) * int64(p.clusterSize)

	return nil
}

func (p *Parser) readMFTRecord(index uint64) ([]byte, error) {
	offset := p.mftStart + int64(index)*int64(p.mftRecSize)
	buf := make([]byte, p.mftRecSize)
	
	if _, err := p.reader.ReadAt(buf, offset); err != nil {
		return nil, err
	}

	// Verify magic
	if string(buf[0:4]) != MFTRecordMagic {
		return nil, fmt.Errorf("invalid MFT record at index %d", index)
	}

	// Apply fixup
	if err := p.applyFixup(buf); err != nil {
		return nil, err
	}

	return buf, nil
}

func (p *Parser) applyFixup(record []byte) error {
	updateSeqOff := binary.LittleEndian.Uint16(record[4:6])
	updateSeqSize := binary.LittleEndian.Uint16(record[6:8])

	if updateSeqSize < 2 {
		return nil
	}

	signature := record[updateSeqOff : updateSeqOff+2]
	
	for i := uint16(1); i < updateSeqSize; i++ {
		pos := int(i)*512 - 2
		if pos >= len(record) {
			break
		}
		// Verify and replace
		if record[pos] == signature[0] && record[pos+1] == signature[1] {
			fixupOffset := updateSeqOff + i*2
			record[pos] = record[fixupOffset]
			record[pos+1] = record[fixupOffset+1]
		}
	}

	return nil
}

func (p *Parser) parseAttributes(record []byte) (*RecoveredFile, error) {
	flags := binary.LittleEndian.Uint16(record[22:24])
	isDeleted := flags&0x01 == 0 // In-use flag not set
	isDir := flags&0x02 != 0

	attrOffset := binary.LittleEndian.Uint16(record[20:22])
	
	file := &RecoveredFile{
		IsDeleted:   isDeleted,
		IsDirectory: isDir,
	}

	offset := int(attrOffset)
	for offset+16 < len(record) {
		attrType := binary.LittleEndian.Uint32(record[offset:])
		if attrType == AttrEnd || attrType == 0 {
			break
		}

		attrLen := binary.LittleEndian.Uint32(record[offset+4:])
		if attrLen == 0 || int(attrLen) > len(record)-offset {
			break
		}

		nonResident := record[offset+8]

		switch attrType {
		case AttrFileName:
			if nonResident == 0 {
				p.parseFileNameAttr(record[offset:offset+int(attrLen)], file)
			}

		case AttrData:
			if nonResident == 1 {
				file.DataRuns = p.parseDataRuns(record[offset : offset+int(attrLen)])
				realSize := binary.LittleEndian.Uint64(record[offset+48:])
				file.Size = realSize
			} else if nonResident == 0 {
				valueLen := binary.LittleEndian.Uint32(record[offset+16:])
				file.Size = uint64(valueLen)
			}
		}

		offset += int(attrLen)
	}

	return file, nil
}

func (p *Parser) parseFileNameAttr(attr []byte, file *RecoveredFile) {
	if len(attr) < 24+66 {
		return
	}

	valueOffset := binary.LittleEndian.Uint16(attr[20:22])
	if int(valueOffset)+66 > len(attr) {
		return
	}

	fnAttr := attr[valueOffset:]
	parentRef := binary.LittleEndian.Uint64(fnAttr[0:8]) & 0x0000FFFFFFFFFFFF
	nameLen := fnAttr[64]
	nameType := fnAttr[65]

	// Skip DOS names (type 2), prefer Win32 or POSIX names
	if nameType == 2 && file.Name != "" {
		return
	}

	if int(66+nameLen*2) > len(fnAttr) {
		return
	}

	// Parse UTF-16LE name
	nameBytes := fnAttr[66 : 66+int(nameLen)*2]
	name := decodeUTF16(nameBytes)

	file.Name = name
	file.ParentRef = parentRef
}

func (p *Parser) parseDataRuns(attr []byte) []DataRun {
	var runs []DataRun

	dataRunsOff := binary.LittleEndian.Uint16(attr[32:34])
	if int(dataRunsOff) >= len(attr) {
		return runs
	}

	data := attr[dataRunsOff:]
	var currentLCN int64

	for i := 0; i < len(data); {
		header := data[i]
		if header == 0 {
			break
		}

		lenBytes := int(header & 0x0F)
		offBytes := int(header >> 4)

		if i+1+lenBytes+offBytes > len(data) {
			break
		}

		// Parse length
		var length uint64
		for j := 0; j < lenBytes; j++ {
			length |= uint64(data[i+1+j]) << (8 * j)
		}

		// Parse offset (signed)
		var offset int64
		if offBytes > 0 {
			for j := 0; j < offBytes; j++ {
				offset |= int64(data[i+1+lenBytes+j]) << (8 * j)
			}
			// Sign extend
			if data[i+lenBytes+offBytes]&0x80 != 0 {
				for j := offBytes; j < 8; j++ {
					offset |= int64(0xFF) << (8 * j)
				}
			}
		}

		currentLCN += offset
		runs = append(runs, DataRun{
			Offset: currentLCN,
			Length: length,
		})

		i += 1 + lenBytes + offBytes
	}

	return runs
}

func decodeUTF16(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	u16 := make([]uint16, len(b)/2)
	for i := range u16 {
		u16[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(u16))
}

// ScanDeletedFiles scans MFT for deleted files
func (p *Parser) ScanDeletedFiles(maxRecords uint64) ([]RecoveredFile, error) {
	var files []RecoveredFile

	fmt.Printf("Scanning MFT records (this may take a while)...\n")

	for i := uint64(0); i < maxRecords; i++ {
		record, err := p.readMFTRecord(i)
		if err != nil {
			continue
		}

		file, err := p.parseAttributes(record)
		if err != nil {
			continue
		}

		if file.Name == "" || file.Name == "." || file.Name == ".." {
			continue
		}

		// Skip system files
		if strings.HasPrefix(file.Name, "$") {
			continue
		}

		file.MFTIndex = i
		p.mftRecords[i] = file

		if file.IsDeleted {
			files = append(files, *file)
		}

		// Progress
		if i > 0 && i%10000 == 0 {
			fmt.Printf("  Scanned %d records, found %d deleted files...\n", i, len(files))
		}
	}

	// Reconstruct paths
	for i := range files {
		files[i].Path = p.reconstructPath(files[i].MFTIndex)
	}

	return files, nil
}

func (p *Parser) reconstructPath(mftIndex uint64) string {
	var parts []string
	visited := make(map[uint64]bool)

	current := mftIndex
	for {
		if visited[current] {
			break
		}
		visited[current] = true

		file, ok := p.mftRecords[current]
		if !ok {
			break
		}

		if file.Name != "" && file.Name != "." {
			parts = append([]string{file.Name}, parts...)
		}

		// Root directory check (parent ref == 5 is root)
		if file.ParentRef == 5 || file.ParentRef == current {
			break
		}

		current = file.ParentRef
	}

	if len(parts) == 0 {
		if file, ok := p.mftRecords[mftIndex]; ok {
			return file.Name
		}
		return fmt.Sprintf("file_%d", mftIndex)
	}

	return filepath.Join(parts...)
}

// RecoverFile extracts file data
func (p *Parser) RecoverFile(file RecoveredFile, outputPath string) error {
	if file.IsDirectory {
		return os.MkdirAll(outputPath, 0755)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	var written uint64
	for _, run := range file.DataRuns {
		if run.Offset == 0 {
			// Sparse run, write zeros
			zeros := make([]byte, run.Length*uint64(p.clusterSize))
			toWrite := min(uint64(len(zeros)), file.Size-written)
			outFile.Write(zeros[:toWrite])
			written += toWrite
			continue
		}

		offset := run.Offset * int64(p.clusterSize)
		for c := uint64(0); c < run.Length && written < file.Size; c++ {
			buf := make([]byte, p.clusterSize)
			if _, err := p.reader.ReadAt(buf, offset+int64(c)*int64(p.clusterSize)); err != nil {
				if err == io.EOF {
					break
				}
				return err
			}

			toWrite := min(uint64(len(buf)), file.Size-written)
			if _, err := outFile.Write(buf[:toWrite]); err != nil {
				return err
			}
			written += toWrite
		}
	}

	return nil
}

// Recover is the main entry point for NTFS recovery
func Recover(reader *disk.Reader, outputDir string, scanOnly bool, carveMode bool) (int, error) {
	parser, err := NewParser(reader)
	if err != nil {
		return 0, err
	}

	fmt.Printf("NTFS filesystem detected\n")
	fmt.Printf("  Bytes per sector: %d\n", parser.bootSector.BytesPerSector)
	fmt.Printf("  Sectors per cluster: %d\n", parser.bootSector.SectorsPerCluster)
	fmt.Printf("  Cluster size: %d bytes\n", parser.clusterSize)
	fmt.Printf("  MFT record size: %d bytes\n", parser.mftRecSize)
	fmt.Printf("  MFT location: cluster %d\n", parser.bootSector.MFTCluster)
	fmt.Println()

	// Estimate max MFT records (use disk size / record size as upper bound)
	diskSize := reader.Size()
	maxRecords := uint64(diskSize) / uint64(parser.mftRecSize)
	if maxRecords > 10000000 {
		maxRecords = 10000000 // Cap at 10M records
	}

	files, err := parser.ScanDeletedFiles(maxRecords)
	if err != nil {
		return 0, err
	}

	fmt.Printf("\nFound %d deleted files:\n\n", len(files))
	for i, f := range files {
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
		if f.IsDirectory || len(f.DataRuns) == 0 {
			continue
		}

		outPath := filepath.Join(outputDir, f.Path)
		if err := parser.RecoverFile(f, outPath); err != nil {
			fmt.Printf("  Failed to recover %s: %v\n", f.Name, err)
			continue
		}
		fmt.Printf("  Recovered: %s\n", outPath)
		recovered++
	}

	return recovered, nil
}

func min(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
