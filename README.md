# Recovery

A fast, read-only data recovery tool for FAT32 and NTFS filesystems written in Go. Recovers deleted files with their original filenames and folder structure.

## Features

- **Read-only**: Never writes to the source drive - completely safe
- **Filesystem-aware recovery**: Parses FAT32/NTFS metadata to recover filenames and folder paths
- **File carving**: Signature-based recovery when filesystem is damaged
- **Fast**: Optimized for large drives with 1MB read buffers
- **Cross-platform**: Works on macOS, Linux, and Windows

## Supported Filesystems

| Filesystem | Deleted Files | Filenames | Folder Structure |
|------------|---------------|-----------|------------------|
| FAT32      | ✅            | ✅ (8.3 + LFN) | ✅              |
| NTFS       | ✅            | ✅            | ✅              |

## Supported File Types (Carving Mode)

When filesystem metadata is damaged, file carving recovers files by their signatures:

| Category | Formats |
|----------|---------|
| Images   | JPEG, PNG, GIF, BMP, WEBP, TIFF |
| Videos   | MP4, AVI, MKV, MOV, WMV, FLV |
| Audio    | MP3, WAV, FLAC, OGG, M4A |
| Documents| PDF, DOCX, XLSX, PPTX, ZIP, RAR, 7Z |
| Database | SQLite |
| Executables | EXE, ELF |

## Installation

```bash
# Clone and build
git clone https://github.com/shubham/recovery.git
cd recovery

# Build CLI tool
go build -o recover ./cmd/recover

# Build interactive TUI tool
go build -o recover-tui ./cmd/recover-tui

# Or install directly
go install github.com/shubham/recovery/cmd/recover@latest
go install github.com/shubham/recovery/cmd/recover-tui@latest
```

## Usage

### Interactive TUI (Recommended for Beginners)

```bash
./recover-tui
```

This launches an interactive terminal interface that guides you through:
1. Selecting source (physical device or disk image)
2. Choosing a device from auto-detected list
3. Selecting recovery mode (scan/recover/carve)
4. Choosing file types to recover
5. Setting output directory

![TUI Screenshot](docs/tui-screenshot.png)

### Command Line Interface

```bash
# Scan for deleted files (doesn't recover, just lists)
./recover -device /dev/disk2s1 -scan

# Recover deleted files to a directory
./recover -device /dev/disk2s1 -output ./recovered

# Use file carving (when filesystem is damaged)
./recover -device /dev/disk2s1 -carve -output ./recovered

# Specify filesystem type manually
./recover -device /dev/disk2s1 -fs ntfs -output ./recovered
```

### Command Line Options

| Flag | Description | Default |
|------|-------------|---------|
| `-device` | Path to device or disk image (required) | - |
| `-output` | Output directory for recovered files | `./recovered` |
| `-fs` | Filesystem type: `auto`, `ntfs`, `fat32` | `auto` |
| `-scan` | Scan only, don't recover files | `false` |
| `-carve` | Use file carving (signature-based recovery) | `false` |

### Platform-Specific Device Paths

**macOS:**
```bash
# List disks
diskutil list

# Use raw device for better performance
./recover -device /dev/rdisk2s1 -scan
```

**Linux:**
```bash
# List disks
lsblk

# Run with sudo for raw device access
sudo ./recover -device /dev/sdb1 -scan
```

**Windows (PowerShell as Admin):**
```powershell
# List disks
Get-Disk

# Use physical drive
.\recover.exe -device \\.\PhysicalDrive1 -scan
```

## Recommended Workflow

### Step 1: Create a Disk Image (Recommended)

Always work on a copy to protect the original drive:

```bash
# macOS/Linux
sudo dd if=/dev/disk2 of=~/drive_backup.img bs=1m status=progress

# Windows (PowerShell as Admin)
.\dd.exe if=\\.\PhysicalDrive1 of=C:\backup\drive.img bs=1M
```

### Step 2: Scan for Deleted Files

```bash
./recover -device ~/drive_backup.img -scan
```

Example output:
```
NTFS filesystem detected
  Bytes per sector: 512
  Sectors per cluster: 8
  Cluster size: 4096 bytes
  MFT record size: 1024 bytes
  MFT location: cluster 786432

Scanning MFT records (this may take a while)...
  Scanned 10000 records, found 23 deleted files...
  Scanned 20000 records, found 47 deleted files...

Found 47 deleted files:

[1] FILE Documents/report.pdf (245678 bytes)
[2] FILE Photos/vacation/IMG_001.jpg (3456789 bytes)
[3] DIR  Photos/vacation
[4] FILE Videos/birthday.mp4 (156789012 bytes)
...
```

### Step 3: Recover Files

```bash
./recover -device ~/drive_backup.img -output ./recovered
```

### Step 4: If Filesystem is Damaged, Use Carving

```bash
./recover -device ~/drive_backup.img -carve -output ./carved_files
```

## How It Works

### Filesystem-Aware Recovery (Default)

1. **FAT32**: Scans directory entries for the deleted marker (`0xE5`). Deleted entries still contain the filename (except first character) and starting cluster.

2. **NTFS**: Parses the Master File Table (MFT) for records where the "in-use" flag is cleared. Extracts `$FILE_NAME` attributes and reconstructs folder paths using parent references.

### File Carving (`-carve` flag)

1. Scans the entire disk for known file signatures (magic bytes)
2. Extracts data from signature until footer or max size
3. Saves with generic names (e.g., `carved_000001.jpg`)

Use carving when:
- Filesystem is corrupted
- Drive was reformatted
- You need to recover from unallocated space

## Project Structure

```
recovery/
├── cmd/
│   ├── recover/             # CLI tool
│   │   └── main.go
│   └── recover-tui/         # Interactive TUI
│       └── main.go
├── internal/
│   ├── device/
│   │   └── device.go        # Device discovery (macOS/Linux/Windows)
│   ├── disk/
│   │   ├── reader.go        # Raw disk I/O
│   │   └── reader_test.go
│   ├── fat32/
│   │   ├── fat32.go         # FAT32 parser
│   │   └── fat32_test.go
│   ├── ntfs/
│   │   ├── ntfs.go          # NTFS MFT parser
│   │   └── ntfs_test.go
│   └── carver/
│       ├── carver.go        # File signature carving
│       └── carver_test.go
├── go.mod
└── README.md
```

## Safety

This tool is **completely read-only**:

- Opens devices with `os.Open()` (read-only mode)
- Never writes to the source device
- All recovered files go to the output directory
- Safe to run multiple times

However, for critical data recovery:
1. **Create a disk image first** before any recovery attempt
2. **Stop using the drive** immediately to prevent overwriting deleted data
3. Consider professional data recovery services for physically damaged drives

## Performance

| Drive Size | Scan Time (SSD) | Scan Time (HDD) |
|------------|-----------------|-----------------|
| 256 GB     | ~8 minutes      | ~28 minutes     |
| 1 TB       | ~33 minutes     | ~2 hours        |

Performance is limited by disk read speed, not CPU.

## Limitations

- **Overwritten data**: Cannot recover files whose clusters have been reused
- **Fragmented deleted files**: FAT32 recovery assumes contiguous clusters for deleted files (FAT entries are zeroed)
- **Encrypted drives**: Does not support BitLocker, FileVault, or LUKS
- **exFAT**: Not yet supported (coming soon)
- **ext4/APFS**: Not yet supported

## Running Tests

```bash
go test ./... -v
```

## Contributing

Contributions are welcome! Areas that need work:

- [ ] exFAT support
- [ ] ext4 support
- [ ] APFS support
- [ ] Better fragmented file recovery for NTFS
- [ ] GUI interface
- [ ] Progress bar with ETA

## License

MIT License - see LICENSE file for details.

## Disclaimer

This software is provided as-is. While it's designed to be safe and read-only, always work on disk images rather than original drives for critical data. The authors are not responsible for any data loss.
