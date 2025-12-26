package device

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Device represents a storage device
type Device struct {
	Path       string
	Name       string
	Size       int64
	SizeHuman  string
	Filesystem string
	Mountpoint string
	Removable  bool
}

// List returns available storage devices
func List() ([]Device, error) {
	switch runtime.GOOS {
	case "darwin":
		return listDarwin()
	case "linux":
		return listLinux()
	case "windows":
		return listWindows()
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func listDarwin() ([]Device, error) {
	cmd := exec.Command("diskutil", "list", "-plist")
	output, err := cmd.Output()
	if err != nil {
		// Fallback to simpler parsing
		return listDarwinSimple()
	}
	_ = output
	return listDarwinSimple()
}

func listDarwinSimple() ([]Device, error) {
	cmd := exec.Command("diskutil", "list")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run diskutil: %w", err)
	}

	var devices []Device
	scanner := bufio.NewScanner(bytes.NewReader(output))

	var currentDisk string
	for scanner.Scan() {
		line := scanner.Text()

		// Main disk line: /dev/disk0 (internal):
		if strings.HasPrefix(line, "/dev/disk") {
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				currentDisk = strings.TrimSuffix(parts[0], ":")
			}
			continue
		}

		// Partition line:    1:    EFI EFI    209.7 MB   disk0s1
		line = strings.TrimSpace(line)
		if len(line) == 0 || !strings.Contains(line, ":") {
			continue
		}

		// Skip header lines
		if strings.HasPrefix(line, "#:") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}

		// Find the device identifier (diskXsY)
		deviceID := ""
		for _, p := range parts {
			if strings.HasPrefix(p, "disk") {
				deviceID = p
				break
			}
		}

		if deviceID == "" {
			continue
		}

		// Get size (look for something like "500.1 GB")
		var sizeStr string
		var sizeBytes int64
		for i, p := range parts {
			if i+1 < len(parts) {
				unit := parts[i+1]
				if unit == "KB" || unit == "MB" || unit == "GB" || unit == "TB" || unit == "B" {
					sizeStr = p + " " + unit
					sizeBytes = parseSize(p, unit)
					break
				}
			}
		}

		// Get filesystem type (usually after the index)
		fsType := ""
		if len(parts) >= 3 {
			fsType = parts[1]
		}

		// Get name
		name := ""
		if len(parts) >= 3 {
			// Name is usually between type and size
			for i := 2; i < len(parts)-2; i++ {
				if name != "" {
					name += " "
				}
				name += parts[i]
			}
		}
		if name == "" {
			name = deviceID
		}

		devices = append(devices, Device{
			Path:       "/dev/" + deviceID,
			Name:       name,
			Size:       sizeBytes,
			SizeHuman:  sizeStr,
			Filesystem: fsType,
			Removable:  !strings.Contains(currentDisk, "internal"),
		})
	}

	// Also add the raw disk devices
	cmd = exec.Command("diskutil", "list", "-plist")
	_ = cmd // We already have the devices

	return devices, nil
}

func listLinux() ([]Device, error) {
	cmd := exec.Command("lsblk", "-b", "-o", "NAME,SIZE,FSTYPE,MOUNTPOINT,RM", "-n", "-l")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run lsblk: %w", err)
	}

	var devices []Device
	scanner := bufio.NewScanner(bytes.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		name := parts[0]
		sizeBytes, _ := strconv.ParseInt(parts[1], 10, 64)

		fsType := ""
		if len(parts) >= 3 {
			fsType = parts[2]
		}

		mountpoint := ""
		if len(parts) >= 4 {
			mountpoint = parts[3]
		}

		removable := false
		if len(parts) >= 5 {
			removable = parts[4] == "1"
		}

		devices = append(devices, Device{
			Path:       "/dev/" + name,
			Name:       name,
			Size:       sizeBytes,
			SizeHuman:  humanSize(sizeBytes),
			Filesystem: fsType,
			Mountpoint: mountpoint,
			Removable:  removable,
		})
	}

	return devices, nil
}

func listWindows() ([]Device, error) {
	cmd := exec.Command("powershell", "-Command",
		"Get-Disk | Select-Object Number,FriendlyName,Size,PartitionStyle | ConvertTo-Json")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run Get-Disk: %w", err)
	}

	// Simple parsing - in production you'd use proper JSON parsing
	var devices []Device
	lines := strings.Split(string(output), "\n")
	for i, line := range lines {
		if strings.Contains(line, "Number") {
			// Extract disk number
			numStr := strings.TrimSpace(strings.Split(line, ":")[1])
			numStr = strings.Trim(numStr, ",")
			num, _ := strconv.Atoi(numStr)

			// Get name from next line
			name := "Unknown"
			if i+1 < len(lines) && strings.Contains(lines[i+1], "FriendlyName") {
				name = strings.TrimSpace(strings.Split(lines[i+1], ":")[1])
				name = strings.Trim(name, `",`)
			}

			devices = append(devices, Device{
				Path:      fmt.Sprintf(`\\.\PhysicalDrive%d`, num),
				Name:      name,
				SizeHuman: "Unknown",
			})
		}
	}

	return devices, nil
}

func parseSize(value, unit string) int64 {
	v, _ := strconv.ParseFloat(value, 64)
	switch unit {
	case "B":
		return int64(v)
	case "KB":
		return int64(v * 1024)
	case "MB":
		return int64(v * 1024 * 1024)
	case "GB":
		return int64(v * 1024 * 1024 * 1024)
	case "TB":
		return int64(v * 1024 * 1024 * 1024 * 1024)
	}
	return 0
}

func humanSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
