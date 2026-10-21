package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type metricValue struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}
type systemMetrics struct {
	StreamID     string        `json:"streamId"`
	Sequence     uint64        `json:"sequence"`
	Timestamp    int64         `json:"timestamp"`
	CPU          []metricValue `json:"cpu"`
	RAM          *float64      `json:"ram"`
	Disks        []metricValue `json:"disks"`
	Fans         []metricValue `json:"fans"`
	Temperatures []metricValue `json:"temperatures"`
}
type cpuCounter struct{ total, idle uint64 }

func readCPUCounters(path string) map[string]cpuCounter {
	data, _ := os.ReadFile(path)
	result := make(map[string]cpuCounter)
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || !strings.HasPrefix(f[0], "cpu") || f[0] == "cpu" {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimPrefix(f[0], "cpu")); err != nil {
			continue
		}
		var c cpuCounter
		valid := true
		// guest and guest_nice are already included in user and nice.
		for i := 1; i < len(f) && i <= 8; i++ {
			v, err := strconv.ParseUint(f[i], 10, 64)
			if err != nil {
				valid = false
				break
			}
			c.total += v
			if i == 4 || i == 5 {
				c.idle += v
			}
		}
		if valid {
			result[f[0]] = c
		}
	}
	return result
}
func cpuUsage(previous, next map[string]cpuCounter) []metricValue {
	result := []metricValue{}
	for name, c := range next {
		p, ok := previous[name]
		if !ok || c.total <= p.total || c.idle < p.idle {
			continue
		}
		total, idle := c.total-p.total, c.idle-p.idle
		if idle > total {
			continue
		}
		result = append(result, metricValue{name, 100 * float64(total-idle) / float64(total)})
	}
	// Keep per-core bars stable even on machines with more than ten CPUs.
	sortMetricsCPU(result)
	return result
}
func sortMetricsCPU(values []metricValue) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0; j-- {
			a, _ := strconv.Atoi(strings.TrimPrefix(values[j-1].Name, "cpu"))
			b, _ := strconv.Atoi(strings.TrimPrefix(values[j].Name, "cpu"))
			if a <= b {
				break
			}
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}
func readMemory(path string) *float64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	fields := map[string]float64{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			v, e := strconv.ParseFloat(f[1], 64)
			if e == nil {
				fields[strings.TrimSuffix(f[0], ":")] = v
			}
		}
	}
	total := fields["MemTotal"]
	available, ok := fields["MemAvailable"]
	if !ok {
		available = fields["MemFree"] + fields["Buffers"] + fields["Cached"] + fields["SReclaimable"] - fields["Shmem"]
	}
	if total <= 0 {
		return nil
	}
	value := 100 * (1 - min(total, max(0, available))/total)
	return &value
}
func readVolumes(path string) []metricValue {
	result := []metricValue{}
	data, _ := os.ReadFile(path)
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		mount := f[1]
		suffix := strings.TrimPrefix(mount, "/volume")
		if suffix == mount {
			continue
		}
		if _, err := strconv.Atoi(suffix); err != nil {
			continue
		}
		if seen[mount] {
			continue
		}
		seen[mount] = true
		var stat syscall.Statfs_t
		if syscall.Statfs(mount, &stat) != nil || stat.Blocks == 0 {
			continue
		}
		result = append(result, metricValue{mount, 100 * (1 - float64(stat.Bfree)/float64(stat.Blocks))})
	}
	return result
}
func readSensors(root string) ([]metricValue, []metricValue) {
	fans, temps := []metricValue{}, []metricValue{}
	dirs, _ := filepath.Glob(filepath.Join(root, "hwmon*"))
	for _, dir := range dirs {
		raw, _ := os.ReadFile(filepath.Join(dir, "name"))
		name := strings.TrimSpace(string(raw))
		for _, kind := range []string{"fan", "temp"} {
			// Only drive sensors belong in Disk Temperature, not CPU or board sensors.
			if kind == "temp" && name != "drivetemp" && name != "nvme" {
				continue
			}
			files, _ := filepath.Glob(filepath.Join(dir, kind+"*_input"))
			for _, file := range files {
				raw, e := os.ReadFile(file)
				if e != nil {
					continue
				}
				v, e := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
				if e != nil || v < 0 {
					continue
				}
				base := strings.TrimSuffix(file, "_input")
				if fault, e := os.ReadFile(base + "_fault"); e == nil && strings.TrimSpace(string(fault)) != "0" {
					continue
				}
				label, _ := os.ReadFile(base + "_label")
				title := fmt.Sprintf("%s %s %s", filepath.Base(dir), name, strings.TrimSpace(string(label)))
				title = strings.TrimSpace(title) + " " + filepath.Base(base)
				if kind == "fan" {
					fans = append(fans, metricValue{title, v})
				} else {
					v /= 1000
					if v <= 150 {
						temps = append(temps, metricValue{title, v})
					}
				}
			}
		}
	}
	return fans, temps
}
