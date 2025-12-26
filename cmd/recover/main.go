package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/shubham/recovery/internal/carver"
	"github.com/shubham/recovery/internal/disk"
	"github.com/shubham/recovery/internal/fat32"
	"github.com/shubham/recovery/internal/ntfs"
)

func main() {
	var (
		device     = flag.String("device", "", "Path to device or image file (e.g., /dev/sdb1, disk.img)")
		outputDir  = flag.String("output", "./recovered", "Output directory for recovered files")
		fsType     = flag.String("fs", "auto", "Filesystem type: auto, ntfs, fat32")
		scanOnly   = flag.Bool("scan", false, "Scan only, don't recover files")
		carveMode  = flag.Bool("carve", false, "Use file carving (signature-based recovery)")
	)
	flag.Parse()

	if *device == "" {
		fmt.Println("Usage: recover -device <path> [-output <dir>] [-fs <type>]")
		fmt.Println("\nExamples:")
		fmt.Println("  recover -device /dev/sdb1 -output ./recovered")
		fmt.Println("  recover -device disk.img -fs ntfs -scan")
		fmt.Println("  recover -device /dev/sdb1 -carve")
		os.Exit(1)
	}

	reader, err := disk.Open(*device)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening device: %v\n", err)
		os.Exit(1)
	}
	defer reader.Close()

	detectedFS := *fsType
	if detectedFS == "auto" {
		detectedFS, err = disk.DetectFilesystem(reader)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not detect filesystem: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Detected filesystem: %s\n", detectedFS)
	}

	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output directory: %v\n", err)
		os.Exit(1)
	}

	var recoveredFiles int

	// Use carving mode if requested (bypasses filesystem parsing)
	if *carveMode {
		fmt.Println("Using file carving mode (signature-based recovery)...")
		recoveredFiles, err = carver.Recover(reader, *outputDir, *scanOnly)
	} else {
		switch detectedFS {
		case "ntfs":
			recoveredFiles, err = ntfs.Recover(reader, *outputDir, *scanOnly, *carveMode)
		case "fat32":
			recoveredFiles, err = fat32.Recover(reader, *outputDir, *scanOnly, *carveMode)
		default:
			fmt.Fprintf(os.Stderr, "Unsupported filesystem: %s\n", detectedFS)
			os.Exit(1)
		}
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Recovery error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nRecovery complete. Found %d deleted files.\n", recoveredFiles)
}
