package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func manager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "darkbloom")
	if err := os.WriteFile(binary, []byte("fake executable; never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	m, err := New(context.Background(), dir, binary, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}
func waitJob(t *testing.T, m *Manager) Job {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		s := m.Status()
		if len(s.Jobs) > 0 && s.Jobs[0].Status != "running" {
			return s.Jobs[0]
		}
		select {
		case <-deadline:
			t.Fatal("job did not finish")
		case <-time.After(time.Millisecond):
		}
	}
}
func TestAllowlist(t *testing.T) {
	for _, r := range []Request{
		{"0123456789abcdef", "download", []string{"--force"}},
		{"0123456789abcdef", "download", []string{"a;touch /tmp/secret"}},
		{"0123456789abcdef", "switch", []string{"../private"}},
		{"0123456789abcdef", "switch", []string{"a/../private"}},
		{"0123456789abcdef", "stop", []string{"model"}},
		{"0123456789abcdef", "switch", nil},
		{"0123456789abcdef", "download", []string{"a", "b"}},
		{"0123456789abcdef", "switch", []string{"a", "a"}},
		{"0123456789abcdef", "shell", nil}, {"short", "stop", nil},
	} {
		if _, err := arguments(r); err == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	for _, tc := range []struct {
		r    Request
		args []string
	}{
		{Request{"0123456789abcdef", "switch", []string{"org/model-a", "model-b"}}, []string{"switch", "--timeout", "600", "--model", "org/model-a", "--model", "model-b"}},
		{Request{"0123456789abcdef", "start", []string{"model-a"}}, []string{"start", "--timeout", "600", "--model", "model-a"}},
		{Request{"0123456789abcdef", "restart", nil}, []string{"restart", "--timeout", "600", "--startup-timeout", "180"}},
	} {
		args, err := arguments(tc.r)
		if err != nil || !reflect.DeepEqual(args, tc.args) {
			t.Fatalf("arguments: %v %v", args, err)
		}
	}
}
func TestSerializedDeduplicatedPersistentJobs(t *testing.T) {
	m := manager(t)
	var calls atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{})
	m.run = func(ctx context.Context, path string, args ...string) ([]byte, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	r := Request{"0123456789abcdef", "stop", []string{}}
	j, err := m.Submit(r)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	same, err := m.Submit(r)
	if err != nil || same.ID != j.ID {
		t.Fatal("duplicate request not reused", err)
	}
	if _, err = m.Submit(Request{r.RequestID, "restart", nil}); err == nil {
		t.Fatal("reused ID accepted different action")
	}
	if _, err = m.Submit(Request{"different-request-id", "restart", nil}); err == nil {
		t.Fatal("concurrent mutation accepted")
	}
	close(release)
	done := waitJob(t, m)
	if done.Status != "succeeded" || calls.Load() != 1 {
		t.Fatal(done, calls.Load())
	}
	info, _ := os.Stat(m.path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("history not private")
	}
	m.Close()
	restored, err := New(context.Background(), filepath.Dir(m.path), m.binary, true)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restored.run = func(context.Context, string, ...string) ([]byte, error) {
		t.Error("duplicate executed after restart")
		return nil, nil
	}
	same, err = restored.Submit(r)
	if err != nil || same.Status != "succeeded" || same.ID != j.ID {
		t.Fatal("history not restored", err)
	}
}
func TestInterruptedJobsNeverReplayAndErrorsAreSanitized(t *testing.T) {
	m := manager(t)
	m.run = func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, errors.New("private token and path")
	}
	_, err := m.Submit(Request{"0123456789abcdef", "restart", nil})
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	j := m.Status().Jobs[0]
	if j.Status != "unconfirmed" || j.Error != "agent_interrupted" {
		t.Fatal(j)
	}
	// Simulate power loss after the durable intent and before a result was saved.
	j.Status = "running"
	body, _ := json.Marshal([]Job{j})
	os.WriteFile(m.path, body, 0600)
	restored, err := New(context.Background(), filepath.Dir(m.path), m.binary, true)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if s := restored.Status(); s.Jobs[0].Status != "unconfirmed" || s.Jobs[0].Error != "agent_interrupted" {
		t.Fatal(s)
	}
}
func TestDisabledMissingBinaryAndPersistenceFailureDoNotExecute(t *testing.T) {
	m := manager(t)
	m.run = func(context.Context, string, ...string) ([]byte, error) {
		t.Error("unexpected execution")
		return nil, nil
	}
	r := Request{"0123456789abcdef", "stop", nil}
	m.enabled = false
	if _, err := m.Submit(r); err == nil {
		t.Fatal("disabled control accepted")
	}
	m.enabled = true
	os.Remove(m.binary)
	if _, err := m.Submit(r); err == nil {
		t.Fatal("missing CLI accepted")
	}
	os.WriteFile(m.binary, []byte("fake"), 0700)
	os.Mkdir(m.path, 0700)
	if _, err := m.Submit(r); err == nil {
		t.Fatal("ran without durable history")
	}
}
func TestModelDiscoveryPreservesLocalWhenCatalogFailsAndStripsPaths(t *testing.T) {
	m := manager(t)
	m.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[1] == "catalog" {
			return nil, errors.New("secret upstream failure")
		}
		if !reflect.DeepEqual(args, []string{"models", "list", "--json", "--all"}) {
			t.Fatal(args)
		}
		return []byte(`{"cacheDirectory":"/private/user/cache","models":[{"id":"model-a","size_bytes":123}]}`), nil
	}
	models, err := m.Models(context.Background())
	if err != nil || len(models.Local) != 1 || models.CatalogError != "catalog_unavailable" {
		t.Fatal(models, err)
	}
	body, _ := json.Marshal(models)
	if string(body) == "" || models.Local[0].ID != "model-a" {
		t.Fatal(string(body))
	}
	m.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"models":[{"id":"--force"}]}`), nil
	}
	models, err = m.Models(context.Background())
	if err != nil || models.LocalError != "models_unavailable" || len(models.Local) != 0 {
		t.Fatal(models, err)
	}
}
