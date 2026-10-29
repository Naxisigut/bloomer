package store

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/upstream"
	"bloomer/agent/migrations"
)

func TestRewardRowsGetSentinelProviderOnCommit(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	round := Round{
		AccountID: "a", StartedAt: at, CompletedAt: at, Cutoff: at.Add(-config.Retention), Mode: "normal",
		Records: []upstream.Earning{
			{AccountID: "a", RecordID: "reward", ProviderID: "", Model: "base_reward", AmountMicroUSD: 500, CreatedAt: at},
			{AccountID: "a", RecordID: "task", ProviderID: "own", Model: "Qwen3-8B", AmountMicroUSD: 100, CreatedAt: at},
			// A blank Provider on a task row is not a reward and must stay blank.
			{AccountID: "a", RecordID: "blank-task", ProviderID: "", Model: "Qwen3-8B", AmountMicroUSD: 100, CreatedAt: at},
			// An already attributed reward keeps upstream's Provider.
			{AccountID: "a", RecordID: "attributed", ProviderID: "own", Model: "base_reward", AmountMicroUSD: 500, CreatedAt: at},
		},
		Summary: upstream.Summary{AccountID: "a", TotalRecordCount: 4},
	}
	if _, err := s.CommitRound(ctx, round); err != nil {
		t.Fatal(err)
	}
	for record, want := range map[string]string{
		"reward": RewardProviderID, "task": "own", "blank-task": "", "attributed": "own",
	} {
		var got string
		if err := s.ReadDB().QueryRowContext(ctx, "SELECT provider_id FROM earnings WHERE record_id=?", record).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s provider_id = %q, want %q", record, got, want)
		}
	}
}

// The upgrade must retag rows collected before this change, because those rows
// already sit in every existing database with an empty provider_id.
func TestMigrationBackfillsRewardProvider(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/agent.db"
	db, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	files := fstest.MapFS{}
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name == "010_reward_provider.sql" {
			continue
		}
		body, err := fs.ReadFile(migrations.Files, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: body}
	}
	if err = migrate(ctx, db, files, path); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO earnings VALUES
  ('a','r1','','pk','j','base_reward',5,0,0,1780000000000000),
  ('a','r2','','pk','j','Base-Reward',5,0,0,1780000000000000),
  ('a','r3','','pk','j','Qwen3-8B',5,0,0,1780000000000000),
  ('a','r4','own','pk','j','base_reward',5,0,0,1780000000000000),
  ('a','r5','','pk','j','base_reward_v2',5,0,0,1780000000000000)`); err != nil {
		t.Fatal(err)
	}

	body, err := fs.ReadFile(migrations.Files, "010_reward_provider.sql")
	if err != nil {
		t.Fatal(err)
	}
	files["010_reward_provider.sql"] = &fstest.MapFile{Data: body}
	if err = migrate(ctx, db, files, path); err != nil {
		t.Fatal(err)
	}
	for record, want := range map[string]string{
		"r1": RewardProviderID, "r2": RewardProviderID, "r3": "", "r4": "own", "r5": "",
	} {
		var got string
		if err = db.QueryRowContext(ctx, "SELECT provider_id FROM earnings WHERE record_id=?", record).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s provider_id = %q, want %q", record, got, want)
		}
	}
}

func TestIsRewardModel(t *testing.T) {
	for _, model := range []string{"base_reward", "baseReward", "BASE-REWARD", " base_reward ", "bonus", "Base.Bonus"} {
		if !IsRewardModel(model) {
			t.Fatalf("%q should be a reward", model)
		}
	}
	for _, model := range []string{"base_reward_v2", "Qwen3-8B", "", "OrgA/base_reward", "reward_model"} {
		if IsRewardModel(model) {
			t.Fatalf("%q should not be a reward", model)
		}
	}
}
