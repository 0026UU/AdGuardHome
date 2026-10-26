// Package cluster adds optional multi-node coordination to AdGuard Home.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// Dependencies contains the host services used by a Cluster.
type Dependencies struct {
	Logger  *slog.Logger
	Local   http.Handler
	Version string
	WorkDir string
}

// Cluster owns node state, HTTP integration, and background workers.
type Cluster struct {
	conf *Config
	deps Dependencies

	configMu sync.Mutex
	mu       sync.RWMutex
	nodes    map[string]*nodeState
	cancel   context.CancelFunc
	server   *http.Server
	workers  sync.WaitGroup
}

// New creates a cluster module without starting network listeners.
func New(conf *Config, deps Dependencies) (*Cluster, error) {
	if conf == nil {
		conf = DefaultConfig()
	}
	conf = cloneConfig(conf)
	applyDefaults(conf)
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Local == nil {
		deps.Local = http.NotFoundHandler()
	}

	c := &Cluster{conf: conf, deps: deps, nodes: map[string]*nodeState{}}
	c.mu.Lock()
	c.reconcileNodesLocked()
	c.mu.Unlock()

	return c, nil
}

func (c *Cluster) reconcileNodesLocked() {
	now := time.Now().UTC()
	local := c.nodes[c.conf.Name]
	if local == nil {
		local = &nodeState{}
		c.nodes[c.conf.Name] = local
	}
	local.status.Name = c.conf.Name
	local.status.APIBase = c.conf.APIBase
	local.status.Online = true
	local.status.Degraded = false
	local.status.Local = true
	local.status.Version = c.deps.Version
	if local.status.LastHeartbeat.IsZero() {
		local.status.LastHeartbeat = now
	}

	valid := map[string]bool{c.conf.Name: true}
	for _, member := range c.conf.Members {
		valid[member.Name] = true
		state := c.nodes[member.Name]
		if state == nil {
			state = &nodeState{}
			c.nodes[member.Name] = state
			state.status.Online = true
		}
		state.status.Name = member.Name
		state.status.APIBase = member.APIBase
		state.status.Local = false
	}
	for name := range c.nodes {
		if !valid[name] {
			delete(c.nodes, name)
		}
	}
}

// Enabled reports whether clustering is active.
func (c *Cluster) Enabled() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.conf != nil && c.conf.Enabled
}

// Middleware adds cluster behavior to the original AGH handler.
func (c *Cluster) Middleware(next http.Handler) http.Handler {
	if !c.Enabled() {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

// Start starts background work and the node API listener.
func (c *Cluster) Start(ctx context.Context) error {
	if !c.Enabled() {
		return nil
	}

	c.mu.Lock()
	if c.server != nil {
		c.mu.Unlock()
		return errors.New("cluster already started")
	}
	listener, err := net.Listen("tcp", c.conf.Listen)
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("listening for cluster nodes: %w", err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.server = &http.Server{
		Handler:           c.NodeAPI(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	server := c.server
	c.workers.Add(2)
	c.mu.Unlock()

	go func() {
		defer c.workers.Done()
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			c.deps.Logger.Error("cluster node API stopped", slog.Any("error", serveErr))
		}
	}()
	go c.healthLoop(workerCtx)

	return nil
}

// Close stops background work and the node API listener.
func (c *Cluster) Close(ctx context.Context) error {
	c.mu.RLock()
	cancel := c.cancel
	server := c.server
	c.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	if server == nil {
		return nil
	}

	err := server.Shutdown(ctx)
	if err != nil {
		_ = server.Close()
	}
	c.workers.Wait()

	return err
}
