package gateway

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

// SystemMetrics holds system resource usage data.
type SystemMetrics struct {
	CPULoad1    float64 `json:"cpu_load_1"`
	CPULoad5    float64 `json:"cpu_load_5"`
	CPULoad15   float64 `json:"cpu_load_15"`
	CPUPercent  float64 `json:"cpu_percent"`
	CPUCores    int     `json:"cpu_cores"`
	MemUsedMB   float64 `json:"mem_used_mb"`
	MemTotalMB  float64 `json:"mem_total_mb"`
	MemPercent  float64 `json:"mem_percent"`
	HeapAllocMB float64 `json:"heap_alloc_mb"`
	SysMB       float64 `json:"sys_mb"`
	GCRuns      uint32  `json:"gc_runs"`
	Goroutines  int     `json:"goroutines"`
	UptimeSec   int64   `json:"uptime_sec"`
	IndicatorMs float64 `json:"indicator_compute_ms"`
	TS          string  `json:"ts"`
}

const indicatorLatencyKey = "metrics:indengine:indicator_compute_ms"

type cpuSample struct {
	idle  uint64
	total uint64
}

var prevCPU cpuSample

func readCPUSample() cpuSample {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSample{}
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			break
		}
		var total, idle uint64
		for i := 1; i < len(fields); i++ {
			v, _ := strconv.ParseUint(fields[i], 10, 64)
			total += v
			if i == 4 {
				idle = v
			}
		}
		return cpuSample{idle: idle, total: total}
	}
	return cpuSample{}
}

// CollectMetrics gathers system resource usage metrics.
func CollectMetrics(start time.Time) SystemMetrics {
	m := SystemMetrics{
		Goroutines: runtime.NumGoroutine(),
		UptimeSec:  int64(time.Since(start).Seconds()),
		TS:         time.Now().UTC().Format(time.RFC3339Nano),
		CPUCores:   runtime.NumCPU(),
	}

	cur := readCPUSample()
	if prevCPU.total > 0 && cur.total > prevCPU.total {
		dTotal := float64(cur.total - prevCPU.total)
		dIdle := float64(cur.idle - prevCPU.idle)
		m.CPUPercent = (1.0 - dIdle/dTotal) * 100.0
	}
	prevCPU = cur

	if f, err := os.Open("/proc/loadavg"); err == nil {
		scanner := bufio.NewScanner(f)
		if scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 3 {
				if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
					m.CPULoad1 = v
				}
				if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
					m.CPULoad5 = v
				}
				if v, err := strconv.ParseFloat(fields[2], 64); err == nil {
					m.CPULoad15 = v
				}
			}
		}
		f.Close()
	}

	if f, err := os.Open("/proc/meminfo"); err == nil {
		var total, available uint64
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if v, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
						total = v
					}
				}
			}
			if strings.HasPrefix(line, "MemAvailable:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if v, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
						available = v
					}
				}
			}
		}
		f.Close()
		if total > 0 {
			used := total - available
			m.MemTotalMB = float64(total) / 1024
			m.MemUsedMB = float64(used) / 1024
			m.MemPercent = float64(used) / float64(total) * 100
		}
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	m.HeapAllocMB = float64(ms.HeapAlloc) / 1024 / 1024
	m.SysMB = float64(ms.Sys) / 1024 / 1024
	m.GCRuns = ms.NumGC

	return m
}

// ReadIndicatorLatency reads the indicator compute latency from Redis.
func ReadIndicatorLatency(ctx context.Context, rdb *goredis.Client) (float64, bool) {
	if rdb == nil {
		return 0, false
	}
	cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	val, err := rdb.Get(cctx, indicatorLatencyKey).Result()
	if err != nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// TFLabel returns a human-readable label for a timeframe in seconds.
func TFLabel(tf int) string {
	if tf < 60 {
		return fmt.Sprintf("%ds", tf)
	}
	if tf < 3600 {
		return fmt.Sprintf("%dm", tf/60)
	}
	return fmt.Sprintf("%dh", tf/3600)
}
