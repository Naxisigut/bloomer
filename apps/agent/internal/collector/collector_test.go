package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/health"
	"bloomer/agent/internal/retention"
	"bloomer/agent/internal/store"
	"bloomer/agent/internal/upstream"
)

var epoch = time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)

type sourceFunc func(context.Context, int, string) (upstream.Snapshot, error)

func (f sourceFunc) Fetch(ctx context.Context, n int, a string) (upstream.Snapshot, error) {
	return f(ctx, n, a)
}
func records(n int, prefix string) []upstream.Earning {
	out := make([]upstream.Earning, n)
	for i := range out {
		out[i] = upstream.Earning{AccountID: "a", RecordID: fmt.Sprintf("%s-%04d", prefix, i), ProviderID: "p", ProviderKey: "pk", JobID: "job", Model: "model", AmountMicroUSD: 7, PromptTokens: 1, CompletionTokens: 2, CreatedAt: epoch.Add(-time.Hour - time.Duration(i)*time.Microsecond)}
	}
	return out
}
func snapshot(n int, rows []upstream.Earning) upstream.Snapshot {
	return upstream.Snapshot{Summary: upstream.Summary{AccountID: "a", TotalRecordCount: 100000, TotalMicroUSD: int64(n)}, Earnings: rows, HistoryLimit: n, RecentCount: len(rows)}
}
func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func seed(t *testing.T, s *store.Store, rows []upstream.Earning, at time.Time) {
	t.Helper()
	_, err := s.CommitRound(context.Background(), store.Round{AccountID: "a", StartedAt: at, CompletedAt: at, Cutoff: at.Add(-config.Retention), Mode: "normal", Records: rows, Summary: upstream.Summary{AccountID: "a", TotalRecordCount: 100000, TotalMicroUSD: 999}})
	if err != nil {
		t.Fatal(err)
	}
}
func engine(s *store.Store, now *time.Time, source sourceFunc) *Collector {
	return &Collector{Store: s, Source: source, Now: func() time.Time { return *now }, Runtime: health.New()}
}
func state(t *testing.T, s *store.Store) store.State {
	t.Helper()
	v, err := s.LoadState(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func scalar(t *testing.T, s *store.Store, query string) int64 {
	t.Helper()
	var n int64
	if err := s.ReadDB().QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNormalNewCorrectionsAndUnchangedRows(t *testing.T) {
	s := newStore(t)
	old := records(42, "old")
	seed(t, s, old, epoch.Add(-time.Minute))
	now := epoch
	incoming := append(records(8, "new"), old...)
	incoming[8].AmountMicroUSD = -5
	c := engine(s, &now, func(_ context.Context, n int, _ string) (upstream.Snapshot, error) { return snapshot(n, incoming), nil })
	got, err := c.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Requests, []int{50}) || got.New != 8 || got.Updated != 1 {
		t.Fatalf("wrong changes: %+v", got)
	}
	if scalar(t, s, "SELECT count(*) FROM earnings") != 50 {
		t.Fatal("wrong persisted count")
	}
	if err = s.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER unchanged_must_not_update BEFORE UPDATE ON earnings BEGIN SELECT RAISE(FAIL,'unexpected update'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	got, err = c.RunOnce(context.Background())
	if err != nil || got.New != 0 || got.Updated != 0 {
		t.Fatalf("unchanged records rewritten: %+v %v", got, err)
	}
}

func TestExpansionUsesRoundStartAndStopsAtCurrentWindow(t *testing.T) {
	for _, stopAt := range []int{100, 300} {
		t.Run(fmt.Sprint(stopAt), func(t *testing.T) {
			s := newStore(t)
			old := records(200, "known")
			seed(t, s, old, epoch.Add(-time.Minute))
			now := epoch
			fresh := records(100, "new")
			c := engine(s, &now, func(_ context.Context, n int, _ string) (upstream.Snapshot, error) {
				var rows []upstream.Earning
				switch n {
				case 50:
					rows = fresh[:50]
				case 100:
					if stopAt == 100 {
						rows = append(append([]upstream.Earning{}, fresh[:70]...), old[:30]...)
					} else {
						rows = fresh
					}
				case 300:
					rows = append(append([]upstream.Earning{}, fresh...), old...)
				default:
					t.Fatalf("unexpected window %d", n)
				}
				return snapshot(n, rows), nil
			})
			got, err := c.RunOnce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := []int{50, 100}
			count := 70
			if stopAt == 300 {
				want = append(want, 300)
				count = 100
			}
			if !reflect.DeepEqual(got.Requests, want) || got.New != count {
				t.Fatalf("incorrect round-start baseline: %+v", got)
			}
			now = now.Add(time.Minute)
			c.Source = sourceFunc(func(_ context.Context, n int, _ string) (upstream.Snapshot, error) {
				return snapshot(n, fresh[:50]), nil
			})
			next, err := c.RunOnce(context.Background())
			if err != nil || !reflect.DeepEqual(next.Requests, []int{50}) {
				t.Fatalf("window leaked into next round: %+v %v", next, err)
			}
		})
	}
}

func TestMovingWindowUnionCanExceedOneThousand(t *testing.T) {
	s := newStore(t)
	seed(t, s, records(1, "known"), epoch.Add(-time.Minute))
	now := epoch
	c := engine(s, &now, func(_ context.Context, n int, _ string) (upstream.Snapshot, error) {
		return snapshot(n, records(n, fmt.Sprint(n))), nil
	})
	result, err := c.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Requests, []int{50, 100, 300, 1000}) || result.New != 1450 {
		t.Fatalf("moving union truncated: %+v", result)
	}
	if scalar(t, s, "SELECT count(*) FROM coverage_gaps") != 1 {
		t.Fatal("saturation gap missing")
	}
}
func TestInitialImportAndDuplicateExpiredFiltering(t *testing.T) {
	s := newStore(t)
	now := epoch
	c := engine(s, &now, func(_ context.Context, n int, _ string) (upstream.Snapshot, error) {
		return snapshot(n, records(n, "initial")), nil
	})
	result, err := c.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "initial" || !reflect.DeepEqual(result.Requests, []int{1000}) || result.New != 1000 || scalar(t, s, "SELECT count(*) FROM coverage_gaps") != 0 {
		t.Fatalf("first import misreported: %+v", result)
	}
	now = now.Add(time.Minute)
	duplicates := records(25, "dup")
	duplicates = append(duplicates, duplicates...)
	c.Source = sourceFunc(func(_ context.Context, n int, _ string) (upstream.Snapshot, error) {
		return snapshot(n, duplicates), nil
	})
	result, err = c.RunOnce(context.Background())
	if err != nil || result.New != 25 || len(result.Requests) != 1 {
		t.Fatalf("duplicates counted as new: %+v %v", result, err)
	}
	expired := records(50, "expired")
	for i := range expired {
		expired[i].CreatedAt = now.Add(-config.Retention - time.Microsecond)
	}
	c.Source = sourceFunc(func(_ context.Context, n int, _ string) (upstream.Snapshot, error) { return snapshot(n, expired), nil })
	result, err = c.RunOnce(context.Background())
	if err != nil || result.New != 0 || len(result.Requests) != 1 {
		t.Fatalf("expired rows expanded window: %+v %v", result, err)
	}
	// Later responses can correct records buffered in earlier windows.
	fresh := records(100, "corrected")
	c.Source = sourceFunc(func(_ context.Context, n int, _ string) (upstream.Snapshot, error) {
		r := fresh
		if n == 50 {
			r = fresh[:50]
		} else if n == 100 {
			r = append([]upstream.Earning{}, fresh[:70]...)
			r[0].AmountMicroUSD = -99
			r = append(r, records(30, "initial")...)
		}
		return snapshot(n, r), nil
	})
	result, err = c.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, "SELECT amount_micro_usd FROM earnings WHERE record_id='corrected-0000'") != -99 {
		t.Fatal("later valid correction was lost")
	}
}
func TestRequestAndCommitFailureDoNotAdvanceSuccess(t *testing.T) {
	for _, kind := range []string{"second_request", "commit"} {
		t.Run(kind, func(t *testing.T) {
			s := newStore(t)
			prior := epoch.Add(-time.Minute)
			seed(t, s, records(1, "known"), prior)
			now := epoch
			c := engine(s, &now, func(_ context.Context, n int, _ string) (upstream.Snapshot, error) {
				if n == 100 {
					return upstream.Snapshot{}, &upstream.Error{Code: "upstream_unavailable"}
				}
				return snapshot(n, records(n, "new")), nil
			})
			if kind == "commit" {
				c.Source = sourceFunc(func(_ context.Context, n int, _ string) (upstream.Snapshot, error) {
					return snapshot(n, records(8, "new")), nil
				})
				err := s.Write(context.Background(), func(tx *sql.Tx) error {
					_, err := tx.Exec(`CREATE TABLE fault_parent(id INTEGER PRIMARY KEY); CREATE TABLE fault_child(id INTEGER REFERENCES fault_parent(id) DEFERRABLE INITIALLY DEFERRED);
    CREATE TRIGGER fail_commit AFTER UPDATE ON account_summary_current BEGIN INSERT INTO fault_child VALUES(1); END;`)
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.RunOnce(context.Background()); err == nil {
				t.Fatal("failure was ignored")
			}
			if !state(t, s).LastSuccess.Equal(prior) || scalar(t, s, "SELECT count(*) FROM earnings") != 1 || scalar(t, s, "SELECT total_micro_usd FROM account_summary_current") != 999 {
				t.Fatal("partial round committed or checkpoint advanced")
			}
			if kind == "commit" && c.Runtime.Snapshot().LastError != "database_unavailable" {
				t.Fatal("commit failure not exposed")
			}
			if kind == "commit" {
				if state(t, s).LastError != "database_unavailable" {
					t.Fatal("commit failure was not persisted after rollback")
				}
				if err := s.Write(context.Background(), func(tx *sql.Tx) error { _, err := tx.Exec("DROP TRIGGER fail_commit"); return err }); err != nil {
					t.Fatal(err)
				}
				now = now.Add(time.Minute)
				retry, err := c.RunOnce(context.Background())
				if err != nil || retry.New != 8 || !state(t, s).LastSuccess.Equal(now) {
					t.Fatalf("failed commit poisoned subsequent transaction: %+v %v", retry, err)
				}
			}
		})
	}
}
func TestRecoveryThresholdAndClockPlan(t *testing.T) {
	for _, tc := range []struct {
		name         string
		state        store.State
		mode, reason string
	}{
		{"first", store.State{}, "initial", "first_import"},
		{"29m59", store.State{LastSuccess: epoch.Add(-29*time.Minute - 59*time.Second)}, "normal", ""},
		{"30m", store.State{LastSuccess: epoch.Add(-30 * time.Minute)}, "recovery", "long_interruption"},
		{"failed25_then_off10", store.State{LastSuccess: epoch.Add(-35 * time.Minute), LastShutdown: epoch.Add(-10 * time.Minute)}, "recovery", "long_interruption"},
		{"future_checkpoint", store.State{LastSuccess: epoch.Add(time.Second)}, "recovery", "clock_anomaly"},
		{"rollback_after_failed_attempt", store.State{LastSuccess: epoch.Add(-time.Minute), ClockHighWater: epoch.Add(time.Minute)}, "recovery", "clock_anomaly"},
		{"missing_checkpoint_with_history", store.State{HistoryInitialized: true}, "recovery", "checkpoint_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode, reason := Plan(tc.state, epoch)
			if mode != tc.mode || reason != tc.reason {
				t.Fatalf("%s %s", mode, reason)
			}
		})
	}
}
func TestRecoveryGapsPersistAndMerge(t *testing.T) {
	s := newStore(t)
	seed(t, s, records(1, "known"), epoch.Add(-time.Hour))
	now := epoch
	prefix := "recovered"
	c := engine(s, &now, func(_ context.Context, n int, _ string) (upstream.Snapshot, error) {
		return snapshot(n, records(10, prefix)), nil
	})
	result, err := c.RunOnce(context.Background())
	if err != nil || result.Mode != "recovery" || !reflect.DeepEqual(result.Requests, []int{1000}) {
		t.Fatalf("recovery: %+v %v", result, err)
	}
	if state(t, s).CoverageStatus != "possible_gap" {
		t.Fatal("disconnected recovery was marked covered")
	}
	now = now.Add(time.Minute)
	if _, err = c.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, "SELECT count(*) FROM coverage_gaps") != 1 || state(t, s).CoverageStatus != "possible_gap" {
		t.Fatal("later success erased gap")
	}
	now = now.Add(30 * time.Minute)
	prefix = "next-recovery"
	if _, err = c.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, "SELECT count(*) FROM coverage_gaps") != 1 {
		t.Fatal("adjacent ongoing gaps were not merged")
	}
}
func TestRateLimitPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	seed(t, s, records(1, "known"), epoch.Add(-time.Minute))
	now := epoch
	retry := now.Add(2 * time.Minute)
	c := engine(s, &now, func(context.Context, int, string) (upstream.Snapshot, error) {
		return upstream.Snapshot{}, &upstream.Error{Code: "rate_limited", RetryAt: retry}
	})
	if _, err = c.RunOnce(context.Background()); err == nil {
		t.Fatal("429 treated as success")
	}
	s.Close()
	s, err = store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	calls := 0
	now = epoch.Add(time.Minute)
	c = engine(s, &now, func(_ context.Context, n int, _ string) (upstream.Snapshot, error) {
		calls++
		return snapshot(n, records(1, "known")), nil
	})
	result, err := c.RunOnce(context.Background())
	if err != nil || !result.Skipped || calls != 0 || !state(t, s).RetryNotBefore.Equal(retry) {
		t.Fatal("restart bypassed throttle")
	}
	now = retry
	result, err = c.RunOnce(context.Background())
	if err != nil || result.Skipped || calls != 1 || state(t, s).LastError != "" {
		t.Fatalf("throttle did not expire: %+v %v", result, err)
	}
}
func TestCancellationAndCleanupCannotChangeRoundBaseline(t *testing.T) {
	s := newStore(t)
	seed(t, s, records(1, "known"), epoch.Add(-time.Minute))
	now := epoch
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := engine(s, &now, func(ctx context.Context, n int, _ string) (upstream.Snapshot, error) {
		close(started)
		<-ctx.Done()
		return upstream.Snapshot{}, ctx.Err()
	})
	finished := make(chan error, 1)
	go func() { _, err := c.RunOnce(ctx); finished <- err }()
	<-started
	cleaner := retention.Cleaner{Store: s, Now: func() time.Time { return now }, Runtime: health.New()}
	short, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if _, err := cleaner.Step(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanup ran inside an active round: %v", err)
	}
	stop()
	cancel()
	if err := <-finished; err == nil {
		t.Fatal("cancelled round succeeded")
	}
	if !state(t, s).LastSuccess.Equal(epoch.Add(-time.Minute)) {
		t.Fatal("cancelled round advanced checkpoint")
	}
	if _, err := cleaner.Step(context.Background()); err != nil {
		t.Fatal("cleanup did not resume after cancellation:", err)
	}
}

func TestAccountBindingAndClockChangeDuringRequest(t *testing.T) {
	s := newStore(t)
	seed(t, s, records(1, "known"), epoch.Add(-time.Minute))
	now := epoch
	c := engine(s, &now, func(_ context.Context, n int, account string) (upstream.Snapshot, error) {
		if account != "a" {
			t.Fatal("persistent account binding not passed to source")
		}
		wrong := snapshot(n, records(1, "wrong"))
		wrong.Summary.AccountID = "b"
		return wrong, nil
	})
	if _, err := c.RunOnce(context.Background()); !errors.Is(err, store.ErrAccountMismatch) {
		t.Fatalf("account changed: %v", err)
	}
	if scalar(t, s, "SELECT total_micro_usd FROM account_summary_current") != 999 {
		t.Fatal("new account overwrote old balance")
	}
	c.Source = sourceFunc(func(_ context.Context, n int, _ string) (upstream.Snapshot, error) {
		now = now.Add(-time.Minute)
		return snapshot(n, records(1, "known")), nil
	})
	if _, err := c.RunOnce(context.Background()); err == nil || c.Runtime.Snapshot().LastError != "clock_changed" {
		t.Fatalf("clock change was hidden: %v", err)
	}
	var limits []int
	c.Source = sourceFunc(func(_ context.Context, n int, _ string) (upstream.Snapshot, error) {
		limits = append(limits, n)
		return snapshot(n, records(1, "known")), nil
	})
	result, err := c.RunOnce(context.Background())
	if err != nil || result.Reason != "clock_anomaly" || !reflect.DeepEqual(limits, []int{1000}) {
		t.Fatalf("rollback did not force recovery: %+v %v", result, err)
	}
}

func TestClockRecoveryReturnsToNormalWindow(t *testing.T) {
	s := newStore(t)
	seed(t, s, records(1, "known"), epoch.Add(-time.Minute))
	now := epoch
	if err := s.RecordStartup(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAttempt(context.Background(), "a", now.Add(time.Hour), "normal", ""); err != nil {
		t.Fatal(err)
	}
	// A future shutdown timestamp is auxiliary and must not poison every later round.
	if err := s.RecordShutdown(context.Background(), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	c := engine(s, &now, func(_ context.Context, n int, _ string) (upstream.Snapshot, error) {
		return snapshot(n, records(1, "known")), nil
	})
	first, err := c.RunOnce(context.Background())
	if err != nil || first.Mode != "recovery" {
		t.Fatalf("clock recovery: %+v %v", first, err)
	}
	now = now.Add(time.Minute)
	next, err := c.RunOnce(context.Background())
	if err != nil || next.Mode != "normal" || !reflect.DeepEqual(next.Requests, []int{50}) {
		t.Fatalf("stale clock state forced endless recovery: %+v %v", next, err)
	}
}

// Cross the real TLS client, JSON decoder, expansion and SQLite transaction.
func TestHTTPSClientToSQLiteCollection(t *testing.T) {
	var phase atomic.Int32
	var mu sync.Mutex
	var requests []int
	secret := strings.Repeat("z", 64)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(401)
			return
		}
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil {
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		requests = append(requests, limit)
		mu.Unlock()
		rows := records(1, "seed")
		if phase.Load() == 1 {
			switch limit {
			case 50:
				rows = records(50, "new")
			case 100:
				rows = records(100, "new")
			case 300:
				rows = append(records(100, "new"), rows...)
			default:
				w.WriteHeader(400)
				return
			}
		}
		wire := make([]map[string]any, 0, len(rows))
		for _, record := range rows {
			wire = append(wire, map[string]any{"id": record.RecordID, "account_id": record.AccountID, "provider_id": record.ProviderID, "provider_key": record.ProviderKey, "job_id": record.JobID, "model": record.Model, "amount_micro_usd": record.AmountMicroUSD, "prompt_tokens": record.PromptTokens, "completion_tokens": record.CompletionTokens, "created_at": record.CreatedAt.Format(time.RFC3339Nano)})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"account_id": "a", "count": 100000, "recent_count": len(wire), "history_limit": limit, "total_micro_usd": 999, "available_balance_micro_usd": 0, "withdrawable_balance_micro_usd": 0, "earnings": wire})
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "official.token")
	if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := upstream.NewClient(server.URL+"/v1/provider/account-earnings", path, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	s := newStore(t)
	now := epoch
	c := &Collector{Store: s, Source: client, Now: func() time.Time { return now }, Runtime: health.New()}
	first, err := c.RunOnce(context.Background())
	if err != nil || first.New != 1 {
		t.Fatalf("initial TLS import failed: %+v %v", first, err)
	}
	phase.Store(1)
	now = now.Add(time.Minute)
	expanded, err := c.RunOnce(context.Background())
	if err != nil || expanded.New != 100 {
		t.Fatalf("TLS expansion failed: %+v %v", expanded, err)
	}
	mu.Lock()
	got := append([]int(nil), requests...)
	mu.Unlock()
	if !reflect.DeepEqual(got, []int{1000, 50, 100, 300}) || scalar(t, s, "SELECT count(*) FROM earnings") != 101 || !state(t, s).LastSuccess.Equal(now) {
		t.Fatalf("HTTP-to-SQLite integration mismatch: %v", got)
	}
}
