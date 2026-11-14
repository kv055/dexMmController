package db

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dexTakerController/internal/exchangeconnectors/dex/robosats"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRoboSatsAtomicAdmissionPostgres(t *testing.T) {
	dsn := os.Getenv("ROBOSATS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set ROBOSATS_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	root, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	rootPool, _ := root.DB()
	defer rootPool.Close()
	schema := "robo_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := root.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer root.Exec("DROP SCHEMA " + schema + " CASCADE")
	gdb, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := gdb.DB()
	defer pool.Close()
	pool.SetMaxOpenConns(16)
	if os.Getenv("ROBOSATS_TEST_PRODUCTION_SCHEMA") == "1" {
		if err := gdb.Exec(`CREATE TABLE transactions (LIKE public.transactions INCLUDING ALL)`).Error; err != nil {
			t.Fatal(err)
		}
	} else {
		if err := gdb.AutoMigrate(&Transaction{}); err != nil {
			t.Fatal(err)
		}
		// Mirror the production enum/check constraints that AutoMigrate alone
		// would miss. In particular, absent hedges are NULL, not an empty enum.
		for _, sql := range []string{`CREATE TYPE cex_provider AS ENUM ('binance','kraken','coinbase')`, `ALTER TABLE transactions ALTER COLUMN cex_hedging_position TYPE cex_provider USING cex_hedging_position::cex_provider`, `ALTER TABLE transactions ADD CONSTRAINT pool_status CHECK(btc_pool_refill_status IS NULL OR btc_pool_refill_status IN ('pending','spot_buy_pending','spot_bought','withdrawal_initiated','awaiting_security_deposit','restored','needs_review'))`} {
			if err := gdb.Exec(sql).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, sql := range []string{`CREATE TABLE offers(id BIGSERIAL PRIMARY KEY,exchange TEXT,offer_id TEXT)`, `CREATE TABLE api_keys(id BIGINT PRIMARY KEY)`, `INSERT INTO api_keys VALUES(1)`} {
		if err := gdb.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	dir := os.Getenv("ROBOSATS_TEST_MIGRATIONS_DIR")
	if dir == "" {
		dir = "../../../dexUnifiedApi/db/migrations"
	}
	for _, name := range []string{"20260916100000_robosats_execution_state.sql", "20260917090100_robosats_durable_admission.sql"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := gdb.Exec(string(raw)).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := NewTransactionRepository(gdb)
	ctx := context.Background()
	makeQuote := func(expiry time.Time) string {
		id := uuid.NewString()
		err := gdb.Exec(`INSERT INTO execution_quotes(id,exchange,coordinator_id,offer_id,payment_method,input_unit,input_amount,fiat_amount,btc_amount,max_bond_sat,max_escrow_sat,bond_fee_sat,escrow_fee_sat,route,expires_at,native_terms) VALUES(?,'robosats','lake','42','SEPA','fiat',100,100,0.001,3000,100100,5,5,'{"provider":"binance","trade_mode":"hedgeless_double_collateral","fiat_currency":"EUR","required_btc_liquidity":0.001031}'::jsonb,?,'{}'::jsonb)`, id, expiry).Error
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	makeSession := func(id string) *robosats.Session {
		return &robosats.Session{Phase: robosats.PhaseCreated, RecoveryEncrypted: "test-only-encrypted-material", Request: robosats.TakeRequest{QuoteID: id, CredentialID: 1, CoordinatorID: "lake", OrderID: "42", MaxBondSat: 3000, MaxEscrowSat: 100100, BondFeeSat: 5, EscrowFeeSat: 5}, Payments: map[string]*robosats.Payment{}}
	}
	// Force a failure after the transaction row has been inserted. All four
	// admission resources must roll back together, including quote binding.
	rollbackID := makeQuote(time.Now().Add(time.Minute))
	bad := makeSession(rollbackID)
	bad.Request.CredentialID = 999
	if _, err := repo.AdmitRoboSats(ctx, rollbackID, json.RawMessage(`{}`), bad); err == nil {
		t.Fatal("missing credential accepted")
	}
	var count int64
	gdb.Table("transactions").Count(&count)
	if count != 0 {
		t.Fatalf("orphan transaction after rollback: %d", count)
	}
	q, err := repo.LoadExecutionQuote(ctx, rollbackID)
	if err != nil || q.TransactionID != 0 {
		t.Fatalf("quote bound after rollback: %+v %v", q, err)
	}
	id := makeQuote(time.Now().Add(time.Minute))
	var wg sync.WaitGroup
	ids, errs := make(chan uint, 12), make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := repo.AdmitRoboSats(ctx, id, json.RawMessage(`{}`), makeSession(id))
			ids <- tx
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var transaction uint
	for got := range ids {
		if transaction == 0 {
			transaction = got
		}
		if got != transaction {
			t.Fatalf("duplicate IDs: %d != %d", got, transaction)
		}
	}
	for _, table := range []string{"transactions", "robosats_trade_sessions", "robosats_execution_jobs"} {
		if err := gdb.Table(table).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("%s rows=%d err=%v", table, count, err)
		}
	}
	s, err := repo.LoadRoboSatsSession(ctx, uint64(transaction))
	if err != nil {
		t.Fatal(err)
	}
	s.Phase = robosats.PhaseNeedsReview
	tx, err := repo.GetTransaction(transaction)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateTransaction(tx); err != nil {
		t.Fatalf("nullable fields failed on update: %v", err)
	}
	if err := repo.SaveRoboSatsSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	other := makeQuote(time.Now().Add(time.Minute))
	if _, err := repo.AdmitRoboSats(ctx, other, json.RawMessage(`{}`), makeSession(other)); err == nil {
		t.Fatal("review state released reservation")
	}
	gdb.Exec(`UPDATE execution_quotes SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute), id)
	if got, err := repo.AdmitRoboSats(ctx, id, json.RawMessage(`{}`), makeSession(id)); err != nil || got != transaction {
		t.Fatalf("bound retry failed: %d %v", got, err)
	}
	unlock, err := repo.LockRoboSatsExecution(ctx, transaction)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := repo.LockRoboSatsExecution(ctx, transaction); err == nil {
		again()
		t.Fatal("two execution owners acquired lock")
	}
	unlock()
	if unlock, err = repo.LockRoboSatsExecution(ctx, transaction); err != nil {
		t.Fatal(err)
	}
	unlock()
	if err := repo.CompleteRoboSats(ctx, transaction); err == nil {
		t.Fatal("incomplete trade released")
	}
	tx.TransactionComplete = true
	tx.BTCPoolRefillStatus = "restored"
	if err := repo.UpdateTransaction(tx); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteRoboSats(ctx, transaction); err != nil {
		t.Fatal(err)
	}
	if jobs, err := repo.RoboSatsJobs(ctx); err != nil || len(jobs) != 0 {
		t.Fatalf("completed job still queued: %+v %v", jobs, err)
	}
	hedgeQuote := makeQuote(time.Now().Add(time.Minute))
	if err := gdb.Exec(`UPDATE execution_quotes SET route=jsonb_set(route,'{trade_mode}','"hedgefull_single_collateral"'::jsonb) WHERE id=?`, hedgeQuote).Error; err != nil {
		t.Fatal(err)
	}
	hedgeID, err := repo.AdmitRoboSats(ctx, hedgeQuote, json.RawMessage(`{}`), makeSession(hedgeQuote))
	if err != nil {
		t.Fatalf("hedgefull admission with nullable pool state: %v", err)
	}
	hedgeTx, err := repo.GetTransaction(hedgeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateTransaction(hedgeTx); err != nil {
		t.Fatalf("hedgefull update with nullable pool state: %v", err)
	}
}
