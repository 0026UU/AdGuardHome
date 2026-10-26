package cluster

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	clustersync "github.com/AdguardTeam/AdGuardHome/internal/cluster/sync"
)

const (
	maxSyncAttempts = 10
	auditLimit      = 1000
)

func (c *Cluster) serveSynchronizedWrite(
	w http.ResponseWriter,
	r *http.Request,
	next http.Handler,
	body []byte,
) {
	capture := newCaptureResponse()
	next.ServeHTTP(capture, cleanedRequest(r, body))
	for key, values := range capture.Header() {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status := capture.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(capture.body.Bytes())
	if status < 200 || status >= 300 {
		return
	}

	query := r.URL.Query()
	query.Del("agh_node")
	req := dispatchRequest{
		Method:      r.Method,
		Path:        r.URL.Path,
		Query:       query.Encode(),
		Body:        append([]byte(nil), body...),
		ContentType: r.Header.Get("Content-Type"),
	}
	if err := c.queueWrite(req); err != nil {
		c.addAudit("write", c.localName(), "all", "failed", "queue_failed")
	}
}

func (c *Cluster) queueWrite(req dispatchRequest) error {
	id, err := operationID()
	if err != nil {
		return err
	}
	c.mu.Lock()
	if !c.conf.Sync.Enabled {
		c.mu.Unlock()
		return nil
	}
	source := c.conf.Name
	now := time.Now().UTC()
	for _, member := range c.conf.Members {
		op := &clustersync.Operation{
			ID:          id,
			Target:      member.Name,
			Method:      req.Method,
			Path:        req.Path,
			Query:       req.Query,
			Body:        append([]byte(nil), req.Body...),
			ContentType: req.ContentType,
			NextAttempt: now,
			CreatedAt:   now,
		}
		c.syncQueue = append(c.syncQueue, op)
		if state := c.nodes[member.Name]; state != nil {
			state.status.PendingSync++
		}
		c.addAuditLocked("write", source, member.Name, "queued", "")
	}
	c.mu.Unlock()
	c.wakeSyncWorker()

	return nil
}

func operationID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generating operation ID: %w", err)
	}

	return hex.EncodeToString(data), nil
}

func (c *Cluster) wakeSyncWorker() {
	select {
	case c.syncWake <- struct{}{}:
	default:
	}
}

func (c *Cluster) syncLoop(ctx context.Context) {
	defer c.workers.Done()
	for {
		c.processAlignments(ctx)
		c.processDueOperations(ctx, time.Now())
		c.mu.RLock()
		interval := c.conf.Sync.Interval.Duration
		c.mu.RUnlock()
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-c.syncWake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

type dueOperation struct {
	job      *clustersync.Operation
	snapshot clustersync.Operation
}

func (c *Cluster) processDueOperations(ctx context.Context, now time.Time) {
	c.mu.Lock()
	due := []dueOperation{}
	for _, job := range c.syncQueue {
		if job.InFlight || job.Attempts >= maxSyncAttempts || job.NextAttempt.IsZero() || job.NextAttempt.After(now) {
			continue
		}
		job.InFlight = true
		snapshot := *job
		snapshot.Body = append([]byte(nil), job.Body...)
		due = append(due, dueOperation{job: job, snapshot: snapshot})
	}
	c.mu.Unlock()

	for _, item := range due {
		code := c.applyOperation(ctx, &item.snapshot)
		c.finishOperation(item.job, code)
	}
}

func (c *Cluster) applyOperation(ctx context.Context, op *clustersync.Operation) string {
	member, ok := c.memberConfig(op.Target)
	if !ok {
		return "node_removed"
	}
	resp, err := c.remoteDispatch(ctx, member, "/control/cluster/apply", dispatchRequest{
		Method:      op.Method,
		Path:        op.Path,
		Query:       op.Query,
		Body:        op.Body,
		ContentType: op.ContentType,
	}, member.Timeout.Duration)
	if err != nil {
		return "request_failed"
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return fmt.Sprintf("remote_status_%d", resp.Status)
	}

	return ""
}

func (c *Cluster) finishOperation(job *clustersync.Operation, code string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	index := -1
	for i, queued := range c.syncQueue {
		if queued == job {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	job.InFlight = false
	source := c.conf.Name
	state := c.nodes[job.Target]
	if code == "" {
		c.syncQueue = append(c.syncQueue[:index], c.syncQueue[index+1:]...)
		if state != nil {
			state.status.PendingSync = max(state.status.PendingSync-1, 0)
			state.status.Degraded = state.status.PendingSync > 0 || !state.status.Online
		}
		c.addAuditLocked("write", source, job.Target, "success", "")

		return
	}
	if code == "node_removed" {
		c.syncQueue = append(c.syncQueue[:index], c.syncQueue[index+1:]...)
		c.addAuditLocked("write", source, job.Target, "skipped", code)

		return
	}

	job.Attempts++
	job.LastError = code
	if state != nil {
		state.status.Degraded = true
	}
	if job.Attempts >= maxSyncAttempts {
		job.NextAttempt = time.Time{}
		c.addAuditLocked("write", source, job.Target, "failed", "retry_exhausted")

		return
	}
	job.NextAttempt = time.Now().UTC().Add(c.conf.Sync.Interval.Duration)
	c.addAuditLocked("write", source, job.Target, "failed", "retry_scheduled")
}

func (c *Cluster) addAudit(operation, source, target, result, message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addAuditLocked(operation, source, target, result, message)
}

func (c *Cluster) addAuditLocked(operation, source, target, result, message string) {
	c.auditLog = append(c.auditLog, clustersync.AuditEntry{
		Time:      time.Now().UTC(),
		Operation: operation,
		Source:    source,
		Target:    target,
		Result:    result,
		Message:   message,
	})
	if len(c.auditLog) > auditLimit {
		copy(c.auditLog, c.auditLog[len(c.auditLog)-auditLimit:])
		c.auditLog = c.auditLog[:auditLimit]
	}
}

func (c *Cluster) auditSnapshot() []clustersync.AuditEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]clustersync.AuditEntry, len(c.auditLog))
	copy(result, c.auditLog)

	return result
}

func (c *Cluster) localName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.conf.Name
}
