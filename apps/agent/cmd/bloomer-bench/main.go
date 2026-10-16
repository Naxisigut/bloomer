// bloomer-bench measures a synthetic retained year. It never reads real credentials.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/health"
	"bloomer/agent/internal/httpapi"
	"bloomer/agent/internal/retention"
	"bloomer/agent/internal/store"
)

func cpu() time.Duration {
	var r syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &r)
	return time.Duration(r.Utime.Sec+r.Stime.Sec)*time.Second + time.Duration(r.Utime.Usec+r.Stime.Usec)*time.Microsecond
}
func disk(path string) map[string]int64 {
	out := map[string]int64{}
	for _, name := range []string{"agent.db", "agent.db-wal", "agent.db-shm", "consistent-backup.db"} {
		if st, err := os.Stat(filepath.Join(path, name)); err == nil {
			out[name] = st.Size()
		}
	}
	return out
}
func main() {
	count := flag.Int("rows", 1825000, "synthetic year row count")
	directory := flag.String("dir", "", "empty benchmark directory; default temporary")
	flag.Parse()
	if *count < 1 {
		panic("rows must be positive")
	}
	dir := *directory
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "bloomer-capacity-")
		if err != nil {
			panic(err)
		}
		defer os.RemoveAll(dir)
	} else {
		if err := os.Mkdir(dir, 0700); err != nil {
			panic(err)
		}
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	start := time.Now()
	s, err := store.Open(ctx, filepath.Join(dir, "agent.db"))
	if err != nil {
		panic(err)
	}
	defer s.Close()
	report := map[string]any{"rows": *count, "platform": runtime.GOOS + "/" + runtime.GOARCH, "go": runtime.Version(), "synthetic": true}
	from := now.Add(-config.Retention + time.Second)
	seedCPU := cpu()
	seedAt := time.Now()
	for offset := 0; offset < *count; offset += 5000 {
		end := offset + 5000
		if end > *count {
			end = *count
		}
		err = s.Write(ctx, func(tx *sql.Tx) error {
			stmt, err := tx.Prepare("INSERT INTO earnings VALUES(?,?,?,?,?,?,?,?,?,?)")
			if err != nil {
				return err
			}
			defer stmt.Close()
			for i := offset; i < end; i++ {
				at := from.Add(time.Duration(float64(i) / float64(*count) * float64(config.Retention-time.Second)))
				_, err = stmt.Exec("00000000-0000-0000-0000-000000000001", fmt.Sprintf("%016d", i), "00000000-0000-0000-0000-000000000002", "pk_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", fmt.Sprintf("00000000-0000-0000-%04d-%012d", i%10000, i), fmt.Sprintf("model-%02d", i%8), 12345, 1000, 100, at.UnixMicro())
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			panic(err)
		}
	}
	err = s.Write(ctx, func(tx *sql.Tx) error {
		account := "00000000-0000-0000-0000-000000000001"
		if _, err := tx.Exec("INSERT INTO account_summary_current VALUES(?,?,?,?,?,?)", account, *count, int64(*count)*12345, 12345, 12345, now.UnixMicro()); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO sync_state(account_id,last_successful_sync_at_us,coverage_start_us,coverage_status) VALUES(?,?,?,'partial')", account, now.UnixMicro(), from.UnixMicro()); err != nil {
			return err
		}
		_, err := tx.Exec("UPDATE maintenance_state SET bound_account_id=?", account)
		return err
	})
	if err != nil {
		panic(err)
	}

	report["seed_wall_ms"] = time.Since(seedAt).Milliseconds()
	report["seed_cpu_ms"] = (cpu() - seedCPU).Milliseconds()
	report["disk_before_queries"] = disk(dir)
	token := "synthetic-benchmark-dashboard-token-0123456789"
	handler := httpapi.New(s, token, "benchmark", "", health.New())
	queries := []string{"/v1/account/overview", "/v1/earnings?limit=500", "/v1/earnings/stats?timezone=Asia%2FShanghai&group_by=hour", "/v1/earnings/stats?timezone=America%2FNew_York&group_by=day", "/v1/earnings/stats?timezone=UTC&group_by=model"}
	var measurements []map[string]any
	for _, path := range queries {
		beforeCPU := cpu()
		before := time.Now()
		request := httptest.NewRequest("GET", path, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var body map[string]any
		if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			panic(err)
		}
		itemCount := 0
		if items, ok := body["items"].([]any); ok {
			itemCount = len(items)
		}
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		measurements = append(measurements, map[string]any{"query": path, "status": response.Code, "wall_ms": time.Since(before).Milliseconds(), "cpu_ms": (cpu() - beforeCPU).Milliseconds(), "response_bytes": response.Body.Len(), "groups_or_items": itemCount, "heap_alloc_bytes": mem.HeapAlloc, "heap_inuse_bytes": mem.HeapInuse})
		if response.Code != 200 {
			panic(fmt.Sprintf("benchmark query failed: %s", response.Body.String()))
		}
		response.Result().Body.Close()
	}
	report["queries"] = measurements
	backupAt := time.Now()
	if err = s.Backup(ctx, filepath.Join(dir, "consistent-backup.db")); err != nil {
		panic(err)
	}
	report["consistent_backup_ms"] = time.Since(backupAt).Milliseconds()
	report["disk_with_backup"] = disk(dir)
	cleaner := retention.Cleaner{Store: s, Now: func() time.Time { return now.Add(8 * 24 * time.Hour) }, Runtime: health.New()}
	cleanAt := time.Now()
	batches := 0
	for {
		batches++
		result, err := cleaner.Step(ctx)
		if err != nil {
			panic(err)
		}
		if !result.More {
			break
		}
	}
	report["cleanup_ms"] = time.Since(cleanAt).Milliseconds()
	report["cleanup_batches"] = batches
	report["disk_after_cleanup"] = disk(dir)
	if err = s.Check(ctx); err != nil {
		panic(err)
	}
	var usage syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	rss := usage.Maxrss
	if runtime.GOOS == "linux" {
		rss *= 1024
	}
	report["max_rss_bytes"] = rss
	report["total_wall_ms"] = time.Since(start).Milliseconds()
	report["total_cpu_ms"] = cpu().Milliseconds()
	data, _ := json.MarshalIndent(report, "", "  ")
	io.WriteString(os.Stdout, string(data)+"\n")
}
