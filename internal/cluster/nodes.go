package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	heartbeatInterval = 30 * time.Second
	maxResponseBytes  = 1 << 20
)

// Status returns a consistent, credential-free cluster snapshot.
func (c *Cluster) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()

	status := Status{
		Enabled: c.conf.Enabled,
		Mode:    c.conf.Mode,
		Name:    c.conf.Name,
		Config:  c.conf.PublicStatus(),
		Nodes:   make([]NodeStatus, 0, len(c.conf.Members)+1),
	}
	if local := c.nodes[c.conf.Name]; local != nil {
		status.Nodes = append(status.Nodes, local.status)
	} else {
		status.Nodes = append(status.Nodes, NodeStatus{
			Name:    c.conf.Name,
			APIBase: c.conf.APIBase,
			Online:  true,
			Local:   true,
			Version: c.deps.Version,
		})
	}

	for _, member := range c.conf.Members {
		if state := c.nodes[member.Name]; state != nil {
			status.Nodes = append(status.Nodes, state.status)
			if state.status.Degraded {
				status.DegradedNodes++
			}
		}
	}

	return status
}

func (c *Cluster) nodeSnapshot(name string) NodeStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if state := c.nodes[name]; state != nil {
		return state.status
	}

	return NodeStatus{}
}

func (c *Cluster) memberConfig(name string) (member Member, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, member = range c.conf.Members {
		if member.Name == name {
			return member, true
		}
	}

	return Member{}, false
}

func (c *Cluster) checkNodes(ctx context.Context) {
	c.mu.RLock()
	names := make([]string, 0, len(c.conf.Members))
	for _, member := range c.conf.Members {
		names = append(names, member.Name)
	}
	c.mu.RUnlock()

	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.checkNode(ctx, name)
		}()
	}
	wg.Wait()
}

func (c *Cluster) checkNode(ctx context.Context, name string) {
	member, ok := c.memberConfig(name)
	if !ok {
		return
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, member.Timeout.Duration)
	defer cancel()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		strings.TrimRight(member.APIBase, "/")+"/control/cluster/ping",
		nil,
	)
	if err == nil {
		req.Header.Set("Authorization", "Bearer "+member.Token)
	}

	var response *http.Response
	if err == nil {
		client := &http.Client{Timeout: member.Timeout.Duration}
		response, err = client.Do(req)
	}
	if err == nil {
		defer func() {
			_ = response.Body.Close()
		}()
	}
	if err == nil && response.StatusCode != http.StatusOK {
		err = fmt.Errorf("unexpected ping status %d", response.StatusCode)
	}

	var pong struct {
		Pong    bool   `json:"pong"`
		Version string `json:"version"`
	}
	if err == nil {
		err = json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&pong)
	}
	if err == nil && !pong.Pong {
		err = fmt.Errorf("invalid ping response")
	}
	if err != nil {
		c.recordNodeFailure(name)
		return
	}

	c.recordNodeSuccess(name, pong.Version, time.Since(started))
}

func (c *Cluster) recordNodeFailure(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.nodes[name]
	if state == nil || state.status.Local {
		return
	}

	state.failures++
	if state.failures >= 3 {
		state.status.Online = false
		state.status.Degraded = true
	}
}

func (c *Cluster) recordNodeSuccess(name, version string, rtt time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.nodes[name]
	if state == nil || state.status.Local {
		return
	}

	wasOffline := !state.status.Online
	state.failures = 0
	state.status.Online = true
	state.status.Degraded = false
	state.status.LastHeartbeat = time.Now().UTC()
	state.status.RTTMillis = max(rtt.Milliseconds(), 0)
	state.status.Version = version
	if wasOffline {
		state.alignmentNeeded = true
	}
}

func (c *Cluster) consumeAlignmentNeeded(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.nodes[name]
	if state == nil || !state.alignmentNeeded {
		return false
	}
	state.alignmentNeeded = false

	return true
}

func (c *Cluster) healthLoop(ctx context.Context) {
	defer c.workers.Done()
	c.checkNodes(ctx)
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.checkNodes(ctx)
		}
	}
}
