package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Integration framework
//
// dumbdock observes well-known container tools (Traefik, Watchtower,
// PruneMate, …) that run beside it. Everything generic lives here: the
// interface, the registry, image matching, override resolution, the per-
// integration attempt gate, and the snapshot store. A vendor file owns only
// its own detection, settings, fetch, and findings.
// ---------------------------------------------------------------------------

// integrationAttemptLimit is the number of availability attempts one
// integration may spend before its API work is disabled for the life of the
// process. It is the single source of the documented three-attempt rule; the
// Traefik alias traefikAttemptLimit points at it.
const integrationAttemptLimit = 3

// traefikAttemptLimit is the Traefik name for the same budget, kept so the
// documented Traefik policy and its tests refer to one value.
const traefikAttemptLimit = integrationAttemptLimit

// integrationMinPollInterval is the cadence the network-polled integrations
// declare from MinPollInterval; a zero MinPollInterval means every cycle.
// Detection still refreshes on every poll; only the vendor HTTP call is skipped.
const integrationMinPollInterval = 30 * time.Second

// Override layers, in the order resolveIntegrationSettings consults them.
const (
	sourceConfig = "config"
	sourceLabel  = "label"
	sourceEnv    = "env"
	sourceAuto   = "auto"
)

// integration is one external container tool dumbdock can observe. Every
// method is side-effect free except Collect, which performs read-only HTTP
// requests, and Issues, which is pure.
type integration interface {
	// ID is the stable lower-case identifier used in config keys, labels,
	// environment variables, issue IDs, and the API route.
	ID() string
	// DisplayName is the operator-facing name, used in log lines and the UI.
	DisplayName() string
	// Description is one sentence naming what dumbdock reads from it.
	Description() string
	// Detect reports whether c is an instance of this integration. It must
	// match the image repository exactly, never a substring.
	Detect(c dockerContainer) bool
	// Settings resolves the effective on/off state, container pin, endpoint,
	// and credentials. inspect may be nil before container discovery.
	Settings(inspect *dockerInspectResponse, cfg *overrideConfig) integrationSettings
	// Collect performs the read-only poll and returns a state with the vendor
	// fields filled. The framework supplies detection, settings, and the gate.
	Collect(rt integrationRuntime, target integrationTarget) integrationState
	// Issues turns a state into findings plus coverage warnings. complete is
	// false only when a collection was attempted and failed.
	Issues(state integrationState) (issues []hostIssue, warnings []string, complete bool)
	// MinPollInterval is the minimum delay between Collect calls. Zero means
	// poll on every refresh cycle.
	MinPollInterval() time.Duration
}

// integrationSettings is the result of the override chain for one integration.
// It never carries a source for the credentials themselves beyond the layer
// name, so a leaked settings value cannot reveal which secret was used.
type integrationSettings struct {
	Enabled   bool
	Forced    bool
	Container string
	URL       string
	Token     string
	Username  string
	Password  string
	// Sources records which layer supplied a field: config, label, env, or
	// auto. Only layer names ever leave the process.
	Sources map[string]string
	// Warnings carries override values that were rejected, so the poll can
	// report them instead of silently ignoring them.
	Warnings []string
}

// source returns the layer that supplied a field.
func (s integrationSettings) source(field string) string {
	if s.Sources == nil {
		return ""
	}
	return s.Sources[field]
}

// integrationRuntime is everything an integration needs to poll. It is built
// once per integration per refresh cycle.
type integrationRuntime struct {
	containers []dockerContainer
	client     *http.Client
	now        time.Time
	gate       *integrationGate
}

// integrationTarget is the resolved target handed to Collect.
type integrationTarget struct {
	Container dockerContainer
	Inspect   *dockerInspectResponse
	Settings  integrationSettings
	// Base is the framework-owned part of the state (identity, detection,
	// enablement). A vendor fills its own fields and returns it.
	Base integrationState
}

// integrationStat is one key figure on an integration card.
type integrationStat struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Hint  string `json:"hint,omitempty"`
}

// integrationSection is one generic table on an integration card. Cells are
// plain strings; the UI escapes every one of them.
type integrationSection struct {
	Title   string     `json:"title"`
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// integrationState is the immutable snapshot published for one integration.
// Readers never mutate it.
type integrationState struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`

	Detected bool `json:"detected"`
	// Enabled is the effective on/off state after overrides. Hidden is set
	// when an override switched the integration off.
	Enabled bool `json:"enabled"`
	Forced  bool `json:"forced"`
	Hidden  bool `json:"hidden,omitempty"`

	ContainerID   string `json:"containerId,omitempty"`
	ContainerName string `json:"containerName,omitempty"`
	Image         string `json:"image,omitempty"`

	URL            string `json:"url,omitempty"`
	URLSource      string `json:"urlSource,omitempty"`
	AuthConfigured bool   `json:"authConfigured"`

	Disabled       bool   `json:"disabled"`
	DisabledReason string `json:"disabledReason,omitempty"`
	Attempts       int    `json:"attempts"`
	AttemptLimit   int    `json:"attemptLimit"`

	Error    string   `json:"error,omitempty"`
	Warnings []string `json:"warnings,omitempty"`

	Stats    []integrationStat    `json:"stats,omitempty"`
	Sections []integrationSection `json:"sections,omitempty"`

	// VendorData carries the vendor's typed payload (for example Traefik's
	// dashboard data) so a vendor handler and issue collector can keep their
	// own shape. It is never serialised: the wire payload is Stats/Sections.
	VendorData any `json:"-"`

	LastChecked time.Time `json:"lastChecked"`
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

var integrationRegistry []integration

// registerIntegration appends an integration to the registry. It runs during
// init only, performs no I/O, and panics on a duplicate id so a copy-paste
// mistake fails at startup instead of silently shadowing another integration.
func registerIntegration(it integration) {
	if it == nil {
		panic("integration: nil registration")
	}
	for _, existing := range integrationRegistry {
		if existing.ID() == it.ID() {
			panic("integration: duplicate id " + it.ID())
		}
	}
	integrationRegistry = append(integrationRegistry, it)
}

// integrationByID resolves a registered integration.
func integrationByID(id string) (integration, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, it := range integrationRegistry {
		if it.ID() == id {
			return it, true
		}
	}
	return nil, false
}

// integrationIDs returns the registered ids in registry order.
func integrationIDs() []string {
	ids := make([]string, 0, len(integrationRegistry))
	for _, it := range integrationRegistry {
		ids = append(ids, it.ID())
	}
	return ids
}

// ---------------------------------------------------------------------------
// Detection and overrides
// ---------------------------------------------------------------------------

// imageRepository normalises an image reference to its repository: digest and
// tag removed, lower-cased, registry and namespace kept.
func imageRepository(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return ""
	}
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	// A tag may only follow the last path separator; a colon earlier is a
	// registry port.
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		image = image[:i]
	}
	return strings.ToLower(image)
}

// repositoryPath drops a registry host from a repository, leaving the
// namespace and name ("ghcr.io/containrrr/watchtower" -> "containrrr/watchtower").
func repositoryPath(repo string) string {
	i := strings.Index(repo, "/")
	if i < 0 {
		return repo
	}
	first := repo[:i]
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		return repo[i+1:]
	}
	return repo
}

// matchesImageRepository reports whether image belongs to the repository want
// (an official name such as "traefik", or a namespaced one such as
// "containrrr/watchtower"). Matching is exact on the repository path, so
// "thomseddon/traefik-forward-auth" and "mywatchtower" never match.
func matchesImageRepository(image, want string) bool {
	repo := imageRepository(image)
	if repo == "" {
		return false
	}
	path := repositoryPath(repo)
	if path == want {
		return true
	}
	// Images from the official library namespace without an owner prefix.
	if !strings.Contains(want, "/") && path == "library/"+want {
		return true
	}
	return false
}

// integrationLabel returns dumbdock.integration.<id>.<field>.
func integrationLabel(labels map[string]string, id, field string) (string, bool) {
	if len(labels) == 0 {
		return "", false
	}
	v, ok := labels["dumbdock.integration."+id+"."+field]
	v = strings.TrimSpace(v)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// integrationEnv returns DUMBDOCK_INTEGRATION_<ID>_<FIELD>. Ids are single
// lower-case words, so iterating the registry makes the name unambiguous.
func integrationEnv(id, field string) (string, bool) {
	v := strings.TrimSpace(os.Getenv("DUMBDOCK_INTEGRATION_" + strings.ToUpper(id) + "_" + field))
	return v, v != ""
}

// parseBoolSetting accepts the values dumbdock documents for on/off settings.
func parseBoolSetting(v string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true, true
	case "false", "0", "no", "off":
		return false, true
	}
	return false, false
}

// resolveIntegrationSettings applies the override chain once:
// config, then container label, then environment variable, then auto.
func resolveIntegrationSettings(id string, cfg *overrideConfig, labels map[string]string) integrationSettings {
	s := integrationSettings{Enabled: true, Sources: map[string]string{}}

	var block *integrationConfig
	if cfg != nil {
		if b, ok := cfg.Integrations[id]; ok {
			block = &b
		}
	}

	// Container pin: a named container is this integration even when its image
	// does not match.
	if block != nil && strings.TrimSpace(block.Container) != "" {
		s.Container = strings.TrimSpace(block.Container)
		s.Sources["container"] = sourceConfig
	} else if v, ok := integrationLabel(labels, id, "container"); ok {
		s.Container = v
		s.Sources["container"] = sourceLabel
	} else if v, ok := integrationEnv(id, "CONTAINER"); ok {
		s.Container = v
		s.Sources["container"] = sourceEnv
	}

	// On/off.
	if block != nil && block.Enabled != nil {
		s.Enabled = *block.Enabled
		s.Sources["enabled"] = sourceConfig
	} else if v, ok := integrationLabel(labels, id, "enabled"); ok {
		if b, valid := parseBoolSetting(v); valid {
			s.Enabled = b
			s.Sources["enabled"] = sourceLabel
		} else {
			s.Warnings = append(s.Warnings, fmt.Sprintf("dumbdock.integration.%s.enabled=%q is not a boolean; ignoring it", id, v))
		}
	} else if v, ok := integrationEnv(id, "ENABLED"); ok {
		if b, valid := parseBoolSetting(v); valid {
			s.Enabled = b
			s.Sources["enabled"] = sourceEnv
		} else {
			s.Warnings = append(s.Warnings, fmt.Sprintf("DUMBDOCK_INTEGRATION_%s_ENABLED=%q is not a boolean; ignoring it", strings.ToUpper(id), v))
		}
	}

	// Endpoint.
	if block != nil && strings.TrimSpace(block.URL) != "" {
		s.URL = strings.TrimSpace(block.URL)
		s.Sources["url"] = sourceConfig
	} else if v, ok := integrationLabel(labels, id, "url"); ok {
		s.URL = v
		s.Sources["url"] = sourceLabel
	} else if v, ok := integrationEnv(id, "URL"); ok {
		s.URL = v
		s.Sources["url"] = sourceEnv
	}

	// Credentials.
	if block != nil && strings.TrimSpace(block.Token) != "" {
		s.Token = strings.TrimSpace(block.Token)
		s.Sources["token"] = sourceConfig
	} else if v, ok := integrationLabel(labels, id, "token"); ok {
		s.Token = v
		s.Sources["token"] = sourceLabel
	} else if v, ok := integrationEnv(id, "TOKEN"); ok {
		s.Token = v
		s.Sources["token"] = sourceEnv
	}
	if block != nil && strings.TrimSpace(block.Username) != "" {
		s.Username = strings.TrimSpace(block.Username)
		s.Sources["username"] = sourceConfig
	} else if v, ok := integrationLabel(labels, id, "user"); ok {
		s.Username = v
		s.Sources["username"] = sourceLabel
	} else if v, ok := integrationEnv(id, "USER"); ok {
		s.Username = v
		s.Sources["username"] = sourceEnv
	}
	if block != nil && strings.TrimSpace(block.Password) != "" {
		s.Password = block.Password
		s.Sources["password"] = sourceConfig
	} else if v, ok := integrationLabel(labels, id, "pass"); ok {
		s.Password = v
		s.Sources["password"] = sourceLabel
	} else if v, ok := integrationEnv(id, "PASS"); ok {
		s.Password = v
		s.Sources["password"] = sourceEnv
	}

	// A configured endpoint with no detected container is a deliberate
	// override; record it so the UI never reports it as auto-detection.
	s.Forced = s.URL != "" && s.source("url") != sourceAuto
	return s
}

// validateIntegrationURL rejects an override endpoint dumbdock must not call:
// non-HTTP schemes, credentials embedded in the URL, and link-local hosts
// (the cloud metadata range).
func validateIntegrationURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme %q is not http or https", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("credentials in the URL are not supported; use token/username/password")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("missing host")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return fmt.Errorf("link-local host %q is not allowed", host)
	}
	return nil
}

// detectIntegrationContainer resolves the container a state should report.
// A pin wins over image matching; without a pin the lowest container name
// among running matches is chosen so the result is deterministic. Stopped
// containers are never selected.
func detectIntegrationContainer(it integration, containers []dockerContainer, s integrationSettings) *dockerContainer {
	pin := s.Container
	if pin == "" {
		// A label on any container can pin the integration globally. Inspecting
		// every container would be expensive, but dockerContainer already
		// carries the labels. The lowest container name wins so the choice is
		// deterministic when several containers carry the label.
		best := ""
		for i := range containers {
			v := strings.TrimSpace(containers[i].Labels["dumbdock.integration."+it.ID()+".container"])
			if v == "" {
				continue
			}
			if name := containerName(containers[i]); best == "" || name < best {
				best = name
				pin = v
			}
		}
	}
	if pin != "" {
		for i := range containers {
			c := containers[i]
			if containerName(c) != pin || c.State != "running" {
				continue
			}
			picked := c
			return &picked
		}
		return nil
	}
	var best *dockerContainer
	for i := range containers {
		c := containers[i]
		if c.State != "running" || !it.Detect(c) {
			continue
		}
		if best == nil || containerName(c) < containerName(*best) {
			picked := c
			best = &picked
		}
	}
	return best
}

// ---------------------------------------------------------------------------
// Boot attempt gate (one per integration)
// ---------------------------------------------------------------------------

// integrationGate bounds availability attempts for one integration. It counts
// a failure only until the first successful poll, which disarms it permanently
// for this process; a disabled gate is cleared only by restarting dumbdock.
type integrationGate struct {
	id             string
	displayName    string
	attempts       int
	disabled       bool
	disabledReason string
	passed         bool
}

// fail records one failed availability attempt and reports the attempt number
// plus whether this attempt disabled the gate. It is a no-op once the gate has
// passed or is already disabled.
func (g *integrationGate) fail(reason string) (attempt int, disabled bool) {
	if g.passed || g.disabled {
		return 0, false
	}
	g.attempts++
	if g.attempts >= integrationAttemptLimit {
		g.disabled = true
		g.disabledReason = reason
		return g.attempts, true
	}
	return g.attempts, false
}

// pass disarms the gate for the rest of the process. It never re-enables a
// disabled gate.
func (g *integrationGate) pass() {
	if g.disabled {
		return
	}
	g.passed = true
}

// shouldAttempt reports whether API work is still permitted.
func (g *integrationGate) shouldAttempt() bool { return !g.disabled }

// recordFailure logs one failed boot attempt and, when this attempt spent the
// last one, the single disable line. A gate that has already passed or is
// disabled records nothing: post-boot failures keep the vendor's state-change
// logging instead.
func (g *integrationGate) recordFailure(reason string) {
	attempt, disabled := g.fail(reason)
	if attempt == 0 {
		return
	}
	log.Printf("%s: attempt %d/%d failed: %s", g.id, attempt, integrationAttemptLimit, reason)
	if disabled {
		log.Printf("%s: unable to access %s API after %d attempts — disabled until dumbdock restarts: %s",
			g.id, g.displayName, integrationAttemptLimit, reason)
	}
}

// ---------------------------------------------------------------------------
// Snapshot store
// ---------------------------------------------------------------------------

// integrationStore publishes the latest state per integration. Snapshots are
// replaced wholesale under one mutex and never mutated after publication, so a
// reader can hold one without racing the poll goroutine.
type integrationStore struct {
	mu         sync.RWMutex
	states     map[string]integrationState
	gates      map[string]*integrationGate
	lastPolled map[string]time.Time
}

func newIntegrationStore() *integrationStore {
	return &integrationStore{
		states:     map[string]integrationState{},
		gates:      map[string]*integrationGate{},
		lastPolled: map[string]time.Time{},
	}
}

// integrationStoreState is the process-wide store.
var integrationStoreState = newIntegrationStore()

// ensure publishes the identity defaults for an integration before its first
// poll, so the API and the UI never show a missing entry.
func (s *integrationStore) ensure(it integration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.states[it.ID()]; ok {
		return
	}
	s.states[it.ID()] = integrationState{
		ID:           it.ID(),
		DisplayName:  it.DisplayName(),
		Description:  it.Description(),
		Enabled:      true,
		AttemptLimit: integrationAttemptLimit,
	}
}

// apply publishes a new snapshot.
func (s *integrationStore) apply(st integrationState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[st.ID] = st
}

// get returns one snapshot.
func (s *integrationStore) get(id string) (integrationState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.states[id]
	return st, ok
}

// all returns every registered integration in registry order. An integration
// without a published state is represented by its identity defaults.
func (s *integrationStore) all() []integrationState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]integrationState, 0, len(integrationRegistry))
	for _, it := range integrationRegistry {
		st, ok := s.states[it.ID()]
		if !ok {
			st = integrationState{
				ID:           it.ID(),
				DisplayName:  it.DisplayName(),
				Description:  it.Description(),
				Enabled:      true,
				AttemptLimit: integrationAttemptLimit,
			}
		}
		out = append(out, st)
	}
	return out
}

// gate returns the process-lifetime gate for an integration, creating it with
// the identity the log lines need.
func (s *integrationStore) gate(it integration) *integrationGate {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.gates[it.ID()]
	if !ok {
		g = &integrationGate{id: it.ID(), displayName: it.DisplayName()}
		s.gates[it.ID()] = g
	}
	return g
}

// lastPoll returns when the integration was last polled.
func (s *integrationStore) lastPoll(id string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastPolled[id]
}

// markPolled records a completed poll attempt.
func (s *integrationStore) markPolled(id string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastPolled[id] = t
}

// due reports whether the integration should be polled at now. Traefik
// declares no minimum, so it stays on every cycle.
func (s *integrationStore) due(it integration, now time.Time) bool {
	min := it.MinPollInterval()
	if min <= 0 {
		return true
	}
	last := s.lastPoll(it.ID())
	return last.IsZero() || now.Sub(last) >= min
}

// reset clears one integration: state, gate, and poll time. It exists for
// tests so each case starts from a process-like state; it is never called from
// production code.
func (s *integrationStore) reset(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, id)
	delete(s.gates, id)
	delete(s.lastPolled, id)
	if it, ok := integrationByID(id); ok {
		s.states[id] = integrationState{
			ID:           it.ID(),
			DisplayName:  it.DisplayName(),
			Description:  it.Description(),
			Enabled:      true,
			AttemptLimit: integrationAttemptLimit,
		}
	}
}

// resetIntegrations clears every integration. Test-only.
func resetIntegrations() {
	for _, id := range integrationIDs() {
		integrationStoreState.reset(id)
	}
}

// ---------------------------------------------------------------------------
// Base state helpers shared by the vendor files
// ---------------------------------------------------------------------------

// baseIntegrationState builds the framework-owned part of a state from the
// integration, its resolved settings, and the detected container.
func baseIntegrationState(it integration, s integrationSettings, c *dockerContainer) integrationState {
	st := integrationState{
		ID:           it.ID(),
		DisplayName:  it.DisplayName(),
		Description:  it.Description(),
		Enabled:      s.Enabled,
		Forced:       s.Forced,
		Hidden:       !s.Enabled,
		AttemptLimit: integrationAttemptLimit,
	}
	if c != nil {
		st.Detected = true
		st.ContainerID = c.ID
		if len(c.ID) > 12 {
			st.ContainerID = c.ID[:12]
		}
		st.ContainerName = containerName(*c)
		st.Image = c.Image
	}
	return st
}

// sortIntegrationStats orders stats by label for a stable UI.
func sortIntegrationStats(stats []integrationStat) []integrationStat {
	out := append([]integrationStat(nil), stats...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Label < out[j-1].Label; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// HTTP payload and handlers
// ---------------------------------------------------------------------------

// integrationsResponse is the payload of GET /api/integrations.
type integrationsResponse struct {
	Integrations []integrationState `json:"integrations"`
	Summary      integrationSummary `json:"summary"`
}

// integrationSummary counts the registry at a glance.
type integrationSummary struct {
	Registered int `json:"registered"`
	Detected   int `json:"detected"`
	Disabled   int `json:"disabled"`
	Hidden     int `json:"hidden"`
}

// integrationStateFor returns the published state, or the identity defaults
// when the integration has not been polled yet.
func integrationStateFor(it integration) integrationState {
	if st, ok := integrationStoreState.get(it.ID()); ok {
		return st
	}
	return integrationState{
		ID:           it.ID(),
		DisplayName:  it.DisplayName(),
		Description:  it.Description(),
		Enabled:      true,
		AttemptLimit: integrationAttemptLimit,
	}
}

// integrationsHandler serves every integration as a cache read.
func integrationsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")

	states := integrationStoreState.all()
	resp := integrationsResponse{Integrations: states, Summary: integrationSummary{Registered: len(states)}}
	for _, st := range states {
		if st.Detected || st.Forced {
			resp.Summary.Detected++
		}
		if st.Disabled {
			resp.Summary.Disabled++
		}
		if st.Hidden {
			resp.Summary.Hidden++
		}
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("integrations: encode response: %v", err)
	}
}

// integrationByIDHandler serves one integration or a 404.
func integrationByIDHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")

	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	it, ok := integrationByID(id)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "unknown integration: " + id})
		return
	}
	json.NewEncoder(w).Encode(integrationStateFor(it))
}

// ---------------------------------------------------------------------------
// Polling
// ---------------------------------------------------------------------------

// refreshIntegrations runs one detection + poll pass over the registry. It is
// called from main.go's refresh, never from an HTTP handler.
func refreshIntegrations(dockerClient *http.Client, containers []dockerContainer, cfg *overrideConfig) {
	now := time.Now()
	// Traefik keeps its documented 10s endpoint timeout; the newer integrations
	// bound their own calls to 8s.
	client := &http.Client{Timeout: 10 * time.Second}

	for _, it := range integrationRegistry {
		integrationStoreState.ensure(it)
		gate := integrationStoreState.gate(it)

		// Resolve what can be resolved without the container (config, env).
		pre := it.Settings(nil, cfg)
		var container *dockerContainer
		if pre.Enabled {
			container = detectIntegrationContainer(it, containers, pre)
		}

		// Switched off by an override: publish visibility only.
		if !pre.Enabled {
			st := baseIntegrationState(it, pre, nil)
			st.Hidden = true
			st.Attempts = gate.attempts
			st.Disabled = gate.disabled
			st.DisabledReason = gate.disabledReason
			st.Warnings = append(st.Warnings, pre.Warnings...)
			st.LastChecked = now
			integrationStoreState.apply(st)
			continue
		}

		// An unsafe or malformed endpoint override is refused before any HTTP
		// work. Config/env values were warned about at startup; this is the
		// enforcement point that also covers container labels.
		if msg := invalidOverrideError(pre.URL); msg != "" {
			st := baseIntegrationState(it, pre, container)
			st.Error = "invalid override URL: " + msg
			st.Warnings = append(st.Warnings, "the URL override was refused; fix integrations."+it.ID()+".url or its label/env equivalent")
			st.Warnings = append(st.Warnings, pre.Warnings...)
			st.LastChecked = now
			integrationStoreState.apply(st)
			continue
		}

		// Nothing to poll yet: no container and no forced endpoint. No attempt
		// is spent, so a late-starting integration still gets its full budget.
		if container == nil && pre.URL == "" {
			st := baseIntegrationState(it, pre, nil)
			st.Attempts = gate.attempts
			st.Disabled = gate.disabled
			st.DisabledReason = gate.disabledReason
			st.Warnings = append(st.Warnings, pre.Warnings...)
			st.LastChecked = now
			integrationStoreState.apply(st)
			continue
		}

		// A disabled gate performs no inspect and no API work until restart;
		// detection stays current because it reads the already-fetched list.
		if !gate.shouldAttempt() {
			prev, _ := integrationStoreState.get(it.ID())
			if prev.ID == "" {
				prev = baseIntegrationState(it, pre, container)
			}
			prev.Enabled = true
			prev.Hidden = false
			prev.Detected = container != nil
			applyContainerIdentity(&prev, container)
			prev.Disabled = true
			prev.DisabledReason = gate.disabledReason
			prev.Attempts = gate.attempts
			prev.AttemptLimit = integrationAttemptLimit
			prev.LastChecked = now
			integrationStoreState.apply(prev)
			continue
		}

		// Vendor poll cadence: refresh detection, keep the last vendor data.
		if !integrationStoreState.due(it, now) {
			prev, _ := integrationStoreState.get(it.ID())
			prev.Enabled = true
			prev.Hidden = false
			prev.Detected = container != nil
			applyContainerIdentity(&prev, container)
			prev.LastChecked = now
			integrationStoreState.apply(prev)
			continue
		}

		var inspect *dockerInspectResponse
		if container != nil {
			inspected, err := inspectContainer(dockerClient, container.ID)
			if err != nil {
				reason := fmt.Sprintf("inspection failed: %v", err)
				st := baseIntegrationState(it, pre, container)
				st.Error = reason
				if gate.passed {
					log.Printf("%s: inspect %s (%s): %v", it.ID(), st.ContainerName, st.ContainerID, err)
				} else {
					gate.recordFailure(reason)
				}
				st.Attempts = gate.attempts
				st.Disabled = gate.disabled
				st.DisabledReason = gate.disabledReason
				st.LastChecked = now
				integrationStoreState.apply(st)
				integrationStoreState.markPolled(it.ID(), now)
				continue
			}
			inspect = inspected
		}

		// Labels from the inspected container can still switch the integration
		// off or retarget it.
		settings := it.Settings(inspect, cfg)
		if !settings.Enabled {
			st := baseIntegrationState(it, settings, nil)
			st.Hidden = true
			st.Warnings = append(st.Warnings, settings.Warnings...)
			st.LastChecked = now
			integrationStoreState.apply(st)
			continue
		}
		if msg := invalidOverrideError(settings.URL); msg != "" {
			st := baseIntegrationState(it, settings, container)
			st.Error = "invalid override URL: " + msg
			st.Warnings = append(st.Warnings, "the URL override was refused; fix integrations."+it.ID()+".url or its label/env equivalent")
			st.Warnings = append(st.Warnings, settings.Warnings...)
			st.LastChecked = now
			integrationStoreState.apply(st)
			continue
		}

		st := it.Collect(
			integrationRuntime{containers: containers, client: client, now: now, gate: gate},
			integrationTarget{
				Container: containerValue(container),
				Inspect:   inspect,
				Settings:  settings,
				Base:      baseIntegrationState(it, settings, container),
			},
		)
		st.Attempts = gate.attempts
		st.Disabled = gate.disabled
		st.DisabledReason = gate.disabledReason
		st.Warnings = append(st.Warnings, settings.Warnings...)
		st.LastChecked = now
		integrationStoreState.apply(st)
		integrationStoreState.markPolled(it.ID(), now)
	}
}

// applyContainerIdentity copies detection identity onto an existing state.
func applyContainerIdentity(st *integrationState, c *dockerContainer) {
	if c == nil {
		st.ContainerID = ""
		st.ContainerName = ""
		st.Image = ""
		return
	}
	st.ContainerID = c.ID
	if len(c.ID) > 12 {
		st.ContainerID = c.ID[:12]
	}
	st.ContainerName = containerName(*c)
	st.Image = c.Image
}

// containerValue dereferences a detection result for the target struct.
func containerValue(c *dockerContainer) dockerContainer {
	if c == nil {
		return dockerContainer{}
	}
	return *c
}

// inspectContainer reads one container's detail from the Docker API. The body
// is bounded by the same limit as every other Docker response.
func inspectContainer(dockerClient *http.Client, containerID string) (*dockerInspectResponse, error) {
	if dockerClient == nil {
		return nil, fmt.Errorf("no docker client")
	}
	resp, err := dockerClient.Get("http://localhost/v1.45/containers/" + url.PathEscape(containerID) + "/json")
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("container inspect returned status %d", resp.StatusCode)
	}

	var inspect dockerInspectResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDockerAPIBodyBytes)).Decode(&inspect); err != nil {
		return nil, fmt.Errorf("decode inspect response: %w", err)
	}
	return &inspect, nil
}

// recordPollFailure publishes an availability failure on the state and spends a
// boot attempt while the gate is still armed. After the gate has passed the
// failure is logged as a state change instead, so a post-boot outage never
// re-disables anything.
func recordPollFailure(gate *integrationGate, st *integrationState, reason string) {
	st.Error = reason
	if gate.shouldAttempt() && !gate.passed {
		gate.recordFailure(reason)
		return
	}
	log.Printf("%s: %s", st.ID, reason)
}

// invalidOverrideError reports whether a URL override must be refused. The
// refusals are enforced at poll time, not only warned about at startup, so a
// container label cannot point the poll at a metadata endpoint.
func invalidOverrideError(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	if err := validateIntegrationURL(raw); err != nil {
		return err.Error()
	}
	return ""
}

// containerAPIURL resolves a vendor endpoint from the container itself: the
// first network IP (network names sorted for determinism) on the given port,
// then the published host port for that same container port.
func containerAPIURL(inspect *dockerInspectResponse, port int) string {
	if inspect == nil || port <= 0 {
		return ""
	}
	names := make([]string, 0, len(inspect.NetworkSettings.Networks))
	for name := range inspect.NetworkSettings.Networks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if ip := inspect.NetworkSettings.Networks[name].IPAddress; ip != "" {
			return fmt.Sprintf("http://%s:%d", ip, port)
		}
	}
	key := fmt.Sprintf("%d/tcp", port)
	if entries, ok := inspect.NetworkSettings.Ports[key]; ok && len(entries) > 0 && entries[0].HostPort != "" {
		return "http://127.0.0.1:" + entries[0].HostPort
	}
	return ""
}

// integrationAuthHeader builds the Authorization value from resolved settings.
// Credentials travel in the header only, never in a query string.
func integrationAuthHeader(s integrationSettings) string {
	if s.Token != "" {
		return "Bearer " + s.Token
	}
	if s.Username != "" || s.Password != "" {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(s.Username+":"+s.Password))
	}
	return ""
}

// ---------------------------------------------------------------------------
// Image reference resolution
// ---------------------------------------------------------------------------

// imageReferenceCache remembers the repository reference for an image ID so a
// container the Docker API reports by digest is still matched, and so the
// same lookup is never repeated across polls. Image IDs are immutable, so the
// answer cannot go stale; the cache is bounded and simply cleared when full.
var imageReferenceCache = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// maxImageReferenceCacheEntries bounds the cache; a host cycling through more
// than this many distinct images clears it rather than growing without limit.
const maxImageReferenceCacheEntries = 512

// looksLikeImageID reports whether an image field is a bare image ID rather
// than a repository reference.
func looksLikeImageID(image string) bool {
	image = strings.TrimSpace(image)
	if strings.HasPrefix(image, "sha256:") {
		return true
	}
	if len(image) != 64 {
		return false
	}
	for _, r := range image {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

// cachedImageReference returns the cached reference and whether it is known.
func cachedImageReference(id string) (string, bool) {
	imageReferenceCache.Lock()
	defer imageReferenceCache.Unlock()
	ref, ok := imageReferenceCache.m[id]
	return ref, ok
}

// storeImageReference caches one lookup result.
func storeImageReference(id, ref string) {
	imageReferenceCache.Lock()
	defer imageReferenceCache.Unlock()
	if len(imageReferenceCache.m) >= maxImageReferenceCacheEntries {
		imageReferenceCache.m = map[string]string{}
	}
	imageReferenceCache.m[id] = ref
}

// resetImageReferenceCache clears the cache. Test-only.
func resetImageReferenceCache() {
	imageReferenceCache.Lock()
	defer imageReferenceCache.Unlock()
	imageReferenceCache.m = map[string]string{}
}

// imageRepoTags resolves an image ID to its first repository tag via the
// Docker API. A dangling image (its tag moved to a newer pull) has none.
func imageRepoTags(dockerClient *http.Client, id string) string {
	if dockerClient == nil || strings.TrimSpace(id) == "" {
		return ""
	}
	resp, err := dockerClient.Get("http://localhost/v1.45/images/" + url.PathEscape(strings.TrimSpace(id)) + "/json")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var image struct {
		RepoTags []string `json:"RepoTags"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDockerAPIBodyBytes)).Decode(&image); err != nil {
		return ""
	}
	for _, tag := range image.RepoTags {
		if tag = strings.TrimSpace(tag); tag != "" && tag != "<none>:<none>" {
			return tag
		}
	}
	return ""
}

// containerConfigImage reads the reference a container was created from. This
// is the fallback for a dangling image, where RepoTags is empty but the
// container still records the name it was started with.
func containerConfigImage(dockerClient *http.Client, containerID string) string {
	inspect, err := inspectContainer(dockerClient, containerID)
	if err != nil {
		return ""
	}
	image := strings.TrimSpace(inspect.Config.Image)
	if image == "" || looksLikeImageID(image) {
		return ""
	}
	return image
}

// lookupImageReference resolves a container's image ID to a usable reference:
// the image's own tags first, then the reference the container was created
// from (which survives its image tag moving to a newer pull).
func lookupImageReference(dockerClient *http.Client, c dockerContainer) string {
	id := strings.TrimSpace(c.ImageID)
	if id == "" {
		id = strings.TrimSpace(c.Image)
	}
	if ref := imageRepoTags(dockerClient, id); ref != "" {
		return ref
	}
	return containerConfigImage(dockerClient, c.ID)
}

// resolveImageReferences replaces an image ID in the container list with the
// image's repository tag. Docker reports the ID for a container created from a
// digest or an image ID, which would otherwise defeat exact repository
// matching (and the dashboard's icon lookup). Only ID-form values are resolved,
// one Docker call per distinct image, cached for the life of the process.
func resolveImageReferences(dockerClient *http.Client, containers []dockerContainer) []dockerContainer {
	out := containers
	copied := false
	for i := range containers {
		if !looksLikeImageID(containers[i].Image) {
			continue
		}
		// The cache key includes the container id: two containers can run the
		// same image ID started from different tags, and a redeploy creates a
		// new container. The cache is bounded, so this cannot grow without limit.
		key := containers[i].ID + "|" + containers[i].ImageID
		ref, ok := cachedImageReference(key)
		if !ok {
			ref = lookupImageReference(dockerClient, containers[i])
			storeImageReference(key, ref)
		}
		if ref == "" {
			continue
		}
		if !copied {
			out = append([]dockerContainer{}, containers...)
			copied = true
		}
		out[i].Image = ref
	}
	return out
}
