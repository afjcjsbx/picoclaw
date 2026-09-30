package plugins

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"sync"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/mcp"
)

// Publish is called serially per plugin, and concurrently across plugins. The
// host must respect agent access policies and register capabilities atomically.
type Publish func(context.Context, Capabilities) []Diagnostic

type Manager struct {
	cfg       config.PluginsConfig
	workspace string
	publish   Publish
	mu        sync.RWMutex
	statuses  map[string]Status
	packages  map[string]*packagePlugin
	startOnce sync.Once
	closeOnce sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
}

func NewManager(cfg config.PluginsConfig, workspace string, publish Publish) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{cfg: cfg, workspace: workspace, publish: publish, statuses: map[string]Status{}, packages: map[string]*packagePlugin{}, ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

func (m *Manager) Start() { m.startOnce.Do(func() { go m.run() }) }
func (m *Manager) Wait(ctx context.Context) error {
	m.Start()
	select {
	case <-m.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) Statuses() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Status, 0, len(m.statuses))
	for _, id := range sortedKeys(m.statuses) {
		status := m.statuses[id]
		status.Diagnostics = append([]Diagnostic(nil), status.Diagnostics...)
		result = append(result, status)
	}
	return result
}
func (m *Manager) diagnostic(d Diagnostic) {
	m.mu.Lock()
	s := m.statuses[d.Plugin]
	s.ID = d.Plugin
	s.Diagnostics = append(s.Diagnostics, d)
	m.statuses[d.Plugin] = s
	m.mu.Unlock()
	logger.WarnCF("plugins", d.Message, map[string]any{"plugin": d.Plugin, "component": d.Component})
}
func (m *Manager) setState(id string, state State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.statuses[id]
	s.State = state
	m.statuses[id] = s
}
func (m *Manager) run() {
	defer close(m.done)
	defer func() {
		if p := recover(); p != nil {
			m.diagnostic(Diagnostic{Component: "discovery", Message: fmt.Sprintf("panic: %v", p)})
		}
	}()
	if !m.cfg.Enabled || m.ctx.Err() != nil {
		return
	}
	items, diagnostics := discover(m.cfg, m.workspace)
	for _, d := range diagnostics {
		m.diagnostic(d)
	}
	semaphore := make(chan struct{}, m.cfg.Parallelism())
	var wg sync.WaitGroup
	for _, item := range items {
		if m.ctx.Err() != nil {
			break
		}
		m.mu.Lock()
		m.statuses[item.id] = Status{ID: item.id, Root: item.root, State: Discovered}
		m.mu.Unlock()
		select {
		case semaphore <- struct{}{}:
		case <-m.ctx.Done():
			break
		}
		if m.ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(item installation) { defer wg.Done(); defer func() { <-semaphore }(); m.load(item) }(item)
	}
	wg.Wait()
}
func (m *Manager) load(item installation) {
	defer func() {
		if p := recover(); p != nil {
			m.diagnostic(Diagnostic{Plugin: item.id, Component: "runtime", Message: fmt.Sprintf("panic: %v", p)})
			m.setState(item.id, Failed)
		}
	}()
	fail := func(component string, err error) {
		m.diagnostic(Diagnostic{Plugin: item.id, Component: component, Message: err.Error()})
		m.setState(item.id, Failed)
	}
	if item.err != nil {
		fail("discovery", item.err)
		return
	}
	m.setState(item.id, Loading)
	data, err := ReadPackageFile(item.root, "plugin.json")
	if err != nil {
		fail("manifest", err)
		return
	}
	manifest, diagnostics, err := ParseManifest(data)
	for _, d := range diagnostics {
		d.Plugin = item.id
		m.diagnostic(d)
	}
	if err != nil {
		fail("manifest", err)
		return
	}
	m.mu.Lock()
	s := m.statuses[item.id]
	s.Name = manifest.Name
	m.statuses[item.id] = s
	m.mu.Unlock()
	dataRoot := m.cfg.DataRoot()
	if !filepath.IsAbs(dataRoot) {
		dataRoot = filepath.Join(m.workspace, dataRoot)
	}
	dataDir, err := prepareDataDirectory(dataRoot, item.id)
	if err != nil {
		// A storage failure prevents local processes, not independent skills
		// or remote MCP servers.
		m.diagnostic(Diagnostic{Plugin: item.id, Component: "data", Message: err.Error()})
	}
	pc := PluginContext{Context: m.ctx, ID: item.id, Root: item.root, DataDir: dataDir, Logger: slog.Default().With("plugin", item.id), Config: maps.Clone(item.entry.Config), AllowHooks: item.entry.AllowHooks, InitTimeout: m.cfg.InitTimeout(), CallTimeout: m.cfg.CallTimeout()}
	p := &packagePlugin{manifest: manifest, manager: mcp.NewManager(), host: m, entry: item.entry}
	m.mu.Lock()
	m.packages[item.id] = p
	m.mu.Unlock()
	if err := p.Initialize(pc); err != nil {
		fail("runtime", err)
		return
	}
	m.mu.RLock()
	hasDiagnostics := len(m.statuses[item.id].Diagnostics) > 0
	m.mu.RUnlock()
	if hasDiagnostics {
		m.setState(item.id, Degraded)
	} else {
		m.setState(item.id, Ready)
	}
}

func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.cancel()
		m.Start()
		<-m.done
		for _, id := range sortedKeys(m.packages) {
			if err := m.packages[id].Close(); err != nil {
				m.diagnostic(Diagnostic{Plugin: id, Component: "shutdown", Message: err.Error()})
			}
			m.setState(id, Closed)
		}
	})
	return nil
}

type packagePlugin struct {
	manifest PluginManifest
	manager  *mcp.Manager
	host     *Manager
	entry    config.PluginEntryConfig
}

var _ Plugin = (*packagePlugin)(nil)

func (p *packagePlugin) Manifest() PluginManifest { return p.manifest }
func (p *packagePlugin) Close() error             { return p.manager.Close() }
func (p *packagePlugin) Initialize(pc PluginContext) error {
	report := func(component string, err error) {
		p.host.diagnostic(Diagnostic{Plugin: pc.ID, Component: component, Message: err.Error()})
	}
	publish := func(cap Capabilities) {
		if pc.Context.Err() != nil || p.host.publish == nil {
			return
		}
		cap.ID, cap.Root, cap.Entry = pc.ID, pc.Root, p.entry
		for _, d := range p.host.publish(pc.Context, cap) {
			d.Plugin = pc.ID
			p.host.diagnostic(d)
		}
	}
	skillEntries, diagnostics := discoverSkills(pc.ID, pc.Root)
	for _, d := range diagnostics {
		p.host.diagnostic(d)
	}
	hooks, diagnostics := discoverHooks(p.manifest, pc)
	for _, d := range diagnostics {
		p.host.diagnostic(d)
	}
	publish(Capabilities{Skills: skillEntries, Hooks: hooks})
	if !componentExists(pc.Root, "mcp.json") {
		return nil
	}
	data, err := ReadPackageFile(pc.Root, "mcp.json")
	if err != nil {
		report("mcp", err)
		return nil
	}
	servers, diagnostics, err := ParseMCP(data)
	for _, d := range diagnostics {
		d.Plugin = pc.ID
		p.host.diagnostic(d)
	}
	if err != nil {
		report("mcp", err)
		return nil
	}
	for _, name := range sortedKeys(servers) {
		if pc.Context.Err() != nil {
			return pc.Context.Err()
		}
		p.loadServer(pc, name, servers[name], publish)
	}
	return nil
}

func (p *packagePlugin) loadServer(pc PluginContext, name string, spec ServerSpec, publish func(Capabilities)) {
	report := func(err error) {
		p.host.diagnostic(Diagnostic{Plugin: pc.ID, Component: "mcp:" + name, Message: err.Error()})
	}
	defer func() {
		if panicValue := recover(); panicValue != nil {
			report(fmt.Errorf("panic: %v", panicValue))
		}
	}()
	if spec.Type == "stdio" && pc.DataDir == "" {
		report(fmt.Errorf("plugin data directory is unavailable"))
		return
	}
	cfg, dir, env, err := spec.RuntimeConfig(pc.Root, pc.DataDir)
	if err != nil {
		report(err)
		return
	}
	if err := p.manager.ConnectPluginServer(pc.Context, name, cfg, mcp.PluginRuntimeOptions{Lifetime: pc.Context, Directory: dir, Environment: env, Timeout: pc.InitTimeout, Stderr: stderrLogger{plugin: pc.ID, server: name}}); err != nil {
		report(err)
		return
	}
	conn, ok := p.manager.GetServer(name)
	if !ok {
		return
	}
	var pluginTools []*PluginTool
	for _, definition := range conn.Tools {
		if definition == nil || definition.Name == "" {
			report(fmt.Errorf("server returned an unnamed tool"))
			continue
		}
		pluginTools = append(pluginTools, newPluginTool(pc.ID, name, definition, p.manager, pc.Context, pc.CallTimeout))
	}
	publish(Capabilities{Tools: pluginTools})
}
