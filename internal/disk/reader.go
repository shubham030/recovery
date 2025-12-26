package disk

import (
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	SectorSize     = 512
	DefaultBufSize = 1024 * 1024 // 1MB buffer for fast reads
)

type Reader struct {
	file       *os.File
	size       int64
	sectorSize int
}

func Open(path string) (*Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open device: %w", err)
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to stat device: %w", err)
	}

	size := stat.Size()

	// For block devices, size might be 0, need to seek to end
	if size == 0 {
		size, err = file.Seek(0, io.SeekEnd)
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("failed to determine device size: %w", err)
		}
		file.Seek(0, io.SeekStart)
	}

	return &Reader{
		file:       file,
		size:       size,
		sectorSize: SectorSize,
	}, nil
}

func (r *Reader) Close() error {
	return r.file.Close()
}

func (r *Reader) Size() int64 {
	return r.size
}

func (r *Reader) SectorSize() int {
	return r.sectorSize
}

func (r *Reader) ReadAt(buf []byte, offset int64) (int, error) {
	return r.file.ReadAt(buf, offset)
}

func (r *Reader) ReadSector(sector int64) ([]byte, error) {
	buf := make([]byte, r.sectorSize)
	_, err := r.ReadAt(buf, sector*int64(r.sectorSize))
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func (r *Reader) ReadSectors(startSector int64, count int) ([]byte, error) {
	buf := make([]byte, count*r.sectorSize)
	_, err := r.ReadAt(buf, startSector*int64(r.sectorSize))
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func (r *Reader) ReadCluster(clusterStart int64, clusterSize int) ([]byte, error) {
	buf := make([]byte, clusterSize)
	_, err := r.ReadAt(buf, clusterStart)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

// Seek wraps file.Seek
func (r *Reader) Seek(offset int64, whence int) (int64, error) {
	return r.file.Seek(offset, whence)
}

// Read wraps file.Read
func (r *Reader) Read(buf []byte) (int, error) {
	return r.file.Read(buf)
}

// DetectFilesystem attempts to identify the filesystem type
func DetectFilesystem(r *Reader) (string, error) {
	// Read first few sectors
	buf := make([]byte, 4096)
	_, err := r.ReadAt(buf, 0)
	if err != nil {
		return "", err
	}

	// Check for NTFS signature at offset 3
	if string(buf[3:7]) == "NTFS" {
		return "ntfs", nil
	}

	// Check for FAT32 signature
	// FAT32 has "FAT32" at offset 82 in boot sector
	if string(buf[82:87]) == "FAT32" {
		return "fat32", nil
	}

	// Alternative FAT32 check at offset 54
	if string(buf[54:59]) == "FAT32" {
		return "fat32", nil
	}

	// Check for FAT16/FAT12
	if string(buf[54:58]) == "FAT1" {
		return "fat16", nil
	}

	return "", errors.New("unknown filesystem")
}
