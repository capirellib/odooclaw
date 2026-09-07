package metering

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type usageQueue struct {
	db        *sql.DB
	client    *controlClient
	wake      chan struct{}
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
}

func newUsageQueue(path string, client *controlClient) (*usageQueue, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// A single connection serializes the short outbox transactions. WAL and a
	// busy timeout keep concurrent Chat/enqueue and worker delivery predictable.
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA synchronous=FULL`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS usage_queue (
		generation_id TEXT PRIMARY KEY,
		payload BLOB NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,
		next_attempt_at INTEGER NOT NULL,
		last_error TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	)`); err != nil {
		db.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	q := &usageQueue{db: db, client: client, wake: make(chan struct{}, 1), cancel: cancel, done: make(chan struct{})}
	go q.run(ctx)
	return q, nil
}

func (q *usageQueue) enqueue(report UsageReport) error {
	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	_, err = q.db.Exec(`INSERT OR IGNORE INTO usage_queue
		(generation_id, payload, next_attempt_at, created_at) VALUES (?, ?, ?, ?)`,
		report.GenerationID, payload, now, now)
	if err == nil {
		select {
		case q.wake <- struct{}{}:
		default:
		}
	}
	return err
}

func (q *usageQueue) run(ctx context.Context) {
	defer close(q.done)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			q.deliverOne(ctx)
		case <-q.wake:
			q.deliverOne(ctx)
		}
	}
}

func (q *usageQueue) deliverOne(ctx context.Context) {
	var id string
	var payload []byte
	var attempts int
	err := q.db.QueryRowContext(ctx, `SELECT generation_id, payload, attempts FROM usage_queue
		WHERE next_attempt_at <= ? ORDER BY created_at LIMIT 1`, time.Now().Unix()).Scan(&id, &payload, &attempts)
	if err == sql.ErrNoRows || err != nil {
		return
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, q.client.http.Timeout)
	defer cancel()
	if err := q.client.sendUsage(deliveryCtx, payload); err == nil {
		_, _ = q.db.ExecContext(ctx, `DELETE FROM usage_queue WHERE generation_id = ?`, id)
		select {
		case q.wake <- struct{}{}:
		default:
		}
		return
	} else {
		attempts++
		seconds := math.Min(3600, math.Pow(2, float64(min(attempts, 12))))
		seconds += rand.Float64() * math.Min(30, seconds/4)
		_, _ = q.db.ExecContext(ctx, `UPDATE usage_queue SET attempts=?, next_attempt_at=?, last_error=? WHERE generation_id=?`,
			attempts, time.Now().Add(time.Duration(seconds)*time.Second).Unix(), fmt.Sprintf("%.1000s", err.Error()), id)
	}
}

func (q *usageQueue) close() {
	q.closeOnce.Do(func() { q.cancel(); <-q.done; _ = q.db.Close() })
}
