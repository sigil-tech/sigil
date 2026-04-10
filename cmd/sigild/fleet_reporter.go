package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/sigil-tech/sigil/internal/config"
	"github.com/sigil-tech/sigil/internal/event"
	"github.com/sigil-tech/sigil/internal/notifier"
	"github.com/sigil-tech/sigil/internal/store"
)

// fleetReport is the anonymized aggregate payload sent to the fleet service.
type fleetReport struct {
	NodeID               string             `json:"node_id"`
	Timestamp            time.Time          `json:"timestamp"`
	Platform             string             `json:"platform"`
	Version              string             `json:"version"`
	TotalEvents          int                `json:"total_events"`
	EventCounts          map[string]int     `json:"event_counts"`
	AIQueryCounts        map[string]int     `json:"ai_query_counts"`
	SuggestionAcceptRate float64            `json:"suggestion_accept_rate"`
	AdoptionTier         int                `json:"adoption_tier"`
	LocalRoutingRatio    float64            `json:"local_routing_ratio"`
	BuildSuccessRate     float64            `json:"build_success_rate"`
	TasksCompleted       int                `json:"tasks_completed"`
	TasksStarted         int                `json:"tasks_started"`
	AvgTaskDurationMin   float64            `json:"avg_task_duration_min"`
	StuckRate            float64            `json:"stuck_rate"`
	AvgSpeedScore        float64            `json:"avg_speed_score"`
	IdleMinutes          float64            `json:"idle_minutes"`
	ActiveMinutes        float64            `json:"active_minutes"`
	MeetingMinutes       float64            `json:"meeting_minutes"`
	BrowserCategories    map[string]float64 `json:"browser_categories"`
	FocusScore           float64            `json:"focus_score"`
	ContextSwitches      int                `json:"context_switches"`
	TopApps              map[string]float64 `json:"top_apps"`
	MLEnabled            bool               `json:"ml_enabled"`
	MLPredictions        int                `json:"ml_predictions"`
}

// fleetRecommendation is a team/org recommendation from the fleet policy.
type fleetRecommendation struct {
	ID         string  `json:"id"`
	Scope      string  `json:"scope"`
	Title      string  `json:"title"`
	Body       string  `json:"body"`
	Confidence float64 `json:"confidence"`
}

// fleetPolicy is the response from GET /api/v1/policy.
type fleetPolicy struct {
	RoutingMode     string                `json:"routing_mode"`
	OrgSettings     map[string]any        `json:"org_settings,omitempty"`
	Recommendations []fleetRecommendation `json:"recommendations,omitempty"`
}

// enrollmentResult is the response from POST /api/v1/enroll.
type enrollmentResult struct {
	OrgID    int            `json:"org_id"`
	TeamID   int            `json:"team_id"`
	OrgName  string         `json:"org_name"`
	TeamName string         `json:"team_name"`
	Role     string         `json:"role"`
	Settings map[string]any `json:"org_settings,omitempty"`
}

// fleetReporter computes anonymized aggregates from the local store and
// sends them to the fleet service. It runs as a background goroutine.
type fleetReporter struct {
	db       *store.Store
	endpoint string
	interval time.Duration
	token    string
	nodeID   string
	log      *slog.Logger
	cfgPath  string
	ntf      *notifier.Notifier

	mu           sync.Mutex
	queue        []fleetReport
	cachedPolicy *fleetPolicy
	enrollment   *enrollmentResult
	lastSent     time.Time
	active       bool
}

func newFleetReporter(db *store.Store, cfg daemonConfig, ntf *notifier.Notifier, log *slog.Logger) *fleetReporter {
	endpoint := cfg.fileCfg.Fleet.Endpoint
	if endpoint == "" {
		endpoint = config.DefaultFleetEndpoint
	}

	interval := time.Hour
	if cfg.fileCfg.Fleet.Interval != "" {
		if d, err := time.ParseDuration(cfg.fileCfg.Fleet.Interval); err == nil && d > 0 {
			interval = d
		}
	}

	token := cfg.fileCfg.Cloud.APIKey

	return &fleetReporter{
		db:       db,
		endpoint: endpoint,
		interval: interval,
		token:    token,
		nodeID:   cfg.fileCfg.Fleet.NodeID,
		log:      log,
		cfgPath:  cfg.configPath,
		ntf:      ntf,
		active:   token != "",
	}
}

// initSeenRecsTable creates the fleet_seen_recs table for persisted dedup.
func (r *fleetReporter) initSeenRecsTable() {
	if r.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := r.db.Exec(ctx, `CREATE TABLE IF NOT EXISTS fleet_seen_recs (
		rec_id  TEXT PRIMARY KEY,
		seen_at INTEGER NOT NULL
	)`); err != nil {
		r.log.Warn("fleet: create seen_recs table", "err", err)
	}
}

// isRecSeen checks the SQLite table for a previously seen recommendation.
func (r *fleetReporter) isRecSeen(ctx context.Context, recID string) bool {
	var seenAt int64
	err := r.db.QueryRow(ctx,
		`SELECT seen_at FROM fleet_seen_recs WHERE rec_id = ?`, recID,
	).Scan(&seenAt)
	if err != nil {
		return false
	}
	return time.Since(time.UnixMilli(seenAt)) < 24*time.Hour
}

// markRecSeen persists a recommendation ID in SQLite.
func (r *fleetReporter) markRecSeen(ctx context.Context, recID string) {
	if _, err := r.db.Exec(ctx,
		`INSERT OR REPLACE INTO fleet_seen_recs (rec_id, seen_at) VALUES (?, ?)`,
		recID, time.Now().UnixMilli(),
	); err != nil {
		r.log.Debug("fleet: mark rec seen", "err", err)
	}
}

// pruneSeenRecs removes entries older than 48 hours.
func (r *fleetReporter) pruneSeenRecs(ctx context.Context) {
	cutoff := time.Now().Add(-48 * time.Hour).UnixMilli()
	if _, err := r.db.Exec(ctx, `DELETE FROM fleet_seen_recs WHERE seen_at < ?`, cutoff); err != nil {
		r.log.Debug("fleet: prune seen recs", "err", err)
	}
}

// run is the main reporter loop.
func (r *fleetReporter) run(ctx context.Context) {
	r.ensureNodeID()
	r.initSeenRecsTable()

	if !r.active {
		r.log.Info("fleet reporter: inactive (no cloud token)")
		return
	}

	// Jitter initial delay.
	jitter := time.Duration(randInt63(int64(r.interval / 4)))
	select {
	case <-time.After(jitter):
	case <-ctx.Done():
		return
	}

	r.log.Info("fleet reporter: starting", "endpoint", r.endpoint, "interval", r.interval)

	// Enroll on first cycle.
	r.enroll(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.cycle(ctx)
		}
	}
}

func (r *fleetReporter) cycle(ctx context.Context) {
	if !r.isActive() {
		return
	}

	report, err := r.computeReport(ctx)
	if err != nil {
		r.log.Warn("fleet: compute report failed", "err", err)
		return
	}

	if err := r.postReport(ctx, report); err != nil {
		r.log.Warn("fleet: send failed, queuing", "err", err)
		r.enqueue(report)
		return
	}

	r.sendQueued(ctx)
	r.fetchPolicy(ctx)
	r.surfaceRecommendations(ctx)
	r.pruneSeenRecs(ctx)
}

// enroll registers this node with the fleet service (idempotent upsert).
func (r *fleetReporter) enroll(ctx context.Context) {
	body, err := json.Marshal(map[string]string{
		"node_id":  r.nodeID,
		"platform": runtime.GOOS,
		"version":  "0.1.0-dev",
	})
	if err != nil {
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost,
		r.endpoint+"/enroll", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.log.Debug("fleet: enrollment failed (will retry)", "err", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		r.mu.Lock()
		r.active = false
		r.mu.Unlock()
		r.log.Warn("fleet: enrollment rejected, deactivating", "status", resp.StatusCode)
		return
	}

	var result enrollmentResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		r.log.Debug("fleet: enrollment decode failed", "err", err)
		return
	}

	r.mu.Lock()
	r.enrollment = &result
	r.mu.Unlock()

	r.log.Info("fleet: enrolled", "org", result.OrgName, "team", result.TeamName, "role", result.Role)
}

// computeReport aggregates local store data into a fleet report.
func (r *fleetReporter) computeReport(ctx context.Context) (fleetReport, error) {
	since := time.Now().Add(-r.interval)
	rpt := fleetReport{
		NodeID:    r.nodeID,
		Timestamp: time.Now(),
		Platform:  runtime.GOOS,
		Version:   "0.1.0-dev",
	}

	// Event counts by kind.
	rpt.EventCounts = make(map[string]int)
	for _, kind := range []event.Kind{
		event.KindFile, event.KindProcess, event.KindGit, event.KindTerminal,
		event.KindHyprland, event.KindAI, event.KindIdle, event.KindBrowser,
	} {
		n, err := r.db.CountEvents(ctx, kind, since)
		if err != nil {
			continue
		}
		rpt.EventCounts[string(kind)] = int(n)
		rpt.TotalEvents += int(n)
	}

	// AI interactions.
	ais, err := r.db.QueryAIInteractions(ctx, since)
	if err == nil {
		rpt.AIQueryCounts = make(map[string]int)
		var localCount int
		for _, ai := range ais {
			rpt.AIQueryCounts[ai.QueryCategory]++
			if ai.Routing == "local" {
				localCount++
			}
		}
		if len(ais) > 0 {
			rpt.LocalRoutingRatio = float64(localCount) / float64(len(ais))
		}
	}

	// Suggestion acceptance (0.0 on error is a safe default for aggregation).
	if rate, err := r.db.QuerySuggestionAcceptanceRate(ctx, since); err == nil {
		rpt.SuggestionAcceptRate = rate
	}

	// Task metrics.
	tm, err := r.db.QueryTaskMetrics(ctx, since)
	if err == nil {
		rpt.TasksCompleted = tm.TasksCompleted
		rpt.TasksStarted = tm.TasksStarted
		rpt.AvgTaskDurationMin = tm.AvgDurationMin
		rpt.StuckRate = tm.StuckRate
	}

	// ML stats.
	ml, err := r.db.QueryMLStats(ctx, since)
	if err == nil {
		rpt.MLEnabled = ml.Predictions > 0
		rpt.MLPredictions = ml.Predictions
	}

	// Adoption tier.
	rpt.AdoptionTier = computeAdoptionTier(rpt.TotalEvents, rpt.SuggestionAcceptRate)

	// Spec 023 enrichments (0.0 on error is safe — means no data for this period).
	if idle, err := r.db.QueryEventDurations(ctx, event.KindIdle, "idle_seconds", since); err == nil {
		rpt.IdleMinutes = idle / 60.0
	}

	if meeting, err := r.db.QueryEventDurations(ctx, event.KindCalendar, "duration_minutes", since); err == nil {
		rpt.MeetingMinutes = meeting
	}

	intervalMinutes := r.interval.Minutes()
	rpt.ActiveMinutes = intervalMinutes - rpt.IdleMinutes - rpt.MeetingMinutes
	if rpt.ActiveMinutes < 0 {
		rpt.ActiveMinutes = 0
	}

	// Browser categories.
	browserCounts, err := r.db.QueryEventPayloadGroupCount(ctx, event.KindBrowser, "category", since)
	if err == nil {
		rpt.BrowserCategories = make(map[string]float64)
		for cat, cnt := range browserCounts {
			if cat != "" {
				rpt.BrowserCategories[cat] = float64(cnt)
			}
		}
	}

	// Context switches.
	focusCount, _ := r.db.CountEvents(ctx, event.KindHyprland, since)
	rpt.ContextSwitches = int(focusCount)

	// Top apps.
	appCounts, err := r.db.QueryEventPayloadGroupCount(ctx, event.KindHyprland, "window_class", since)
	if err == nil {
		rpt.TopApps = make(map[string]float64)
		for app, cnt := range appCounts {
			if app != "" {
				rpt.TopApps[app] = float64(cnt) * 2.0 / 60.0
			}
		}
	}

	// Focus score heuristic.
	if rpt.ActiveMinutes > 0 && rpt.ContextSwitches > 0 {
		switchesPerMinute := float64(rpt.ContextSwitches) / rpt.ActiveMinutes
		rpt.FocusScore = 100.0 * (1.0 - min(switchesPerMinute/2.0, 1.0))
	} else if rpt.ActiveMinutes > 0 {
		rpt.FocusScore = 100.0
	}

	return rpt, nil
}

// postReport sends a report to the fleet API.
func (r *fleetReporter) postReport(ctx context.Context, report fleetReport) error {
	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost,
		r.endpoint+"/reports", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send report: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		r.mu.Lock()
		r.active = false
		r.mu.Unlock()
		r.log.Warn("fleet: subscription rejected, deactivating", "status", resp.StatusCode)
		return fmt.Errorf("fleet rejected: %d", resp.StatusCode)
	}

	if resp.StatusCode >= 300 {
		return fmt.Errorf("fleet returned %d", resp.StatusCode)
	}

	r.mu.Lock()
	r.lastSent = time.Now()
	r.mu.Unlock()

	r.log.Info("fleet: report sent", "events", report.TotalEvents)
	return nil
}

func (r *fleetReporter) enqueue(report fleetReport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.queue) >= 24 {
		r.queue = r.queue[1:]
	}
	r.queue = append(r.queue, report)
}

func (r *fleetReporter) sendQueued(ctx context.Context) {
	r.mu.Lock()
	if len(r.queue) == 0 {
		r.mu.Unlock()
		return
	}
	pending := make([]fleetReport, len(r.queue))
	copy(pending, r.queue)
	r.mu.Unlock()

	var sent int
	for _, report := range pending {
		if err := r.postReport(ctx, report); err != nil {
			break
		}
		sent++
	}

	if sent > 0 {
		r.mu.Lock()
		r.queue = r.queue[sent:]
		r.mu.Unlock()
	}
}

// fetchPolicy gets the routing policy and team recommendations from fleet.
func (r *fleetReporter) fetchPolicy(ctx context.Context) {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/policy?node_id=%s", r.endpoint, r.nodeID)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+r.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	var policy fleetPolicy
	if err := json.NewDecoder(resp.Body).Decode(&policy); err != nil {
		return
	}

	r.mu.Lock()
	r.cachedPolicy = &policy
	r.mu.Unlock()
}

// surfaceRecommendations pushes new team/org recommendations to the notifier.
func (r *fleetReporter) surfaceRecommendations(ctx context.Context) {
	r.mu.Lock()
	policy := r.cachedPolicy
	r.mu.Unlock()

	if policy == nil || r.ntf == nil {
		return
	}

	for _, rec := range policy.Recommendations {
		if r.isRecSeen(ctx, rec.ID) {
			continue
		}

		category := "team_insight"
		if rec.Scope == "org" {
			category = "org_insight"
		}

		r.ntf.Surface(notifier.Suggestion{
			Category:   category,
			Confidence: rec.Confidence,
			Title:      rec.Title,
			Body:       rec.Body,
		})

		r.markRecSeen(ctx, rec.ID)
	}
}

// orgSettingsNotificationFloor returns the org-wide notification minimum,
// or -1 if not set.
func (r *fleetReporter) orgSettingsNotificationFloor() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cachedPolicy == nil || r.cachedPolicy.OrgSettings == nil {
		return -1
	}

	if floor, ok := r.cachedPolicy.OrgSettings["notification_level_floor"].(float64); ok {
		return int(floor)
	}
	return -1
}

func (r *fleetReporter) ensureNodeID() {
	if r.nodeID != "" {
		return
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		r.nodeID = "n_unknown"
		return
	}
	r.nodeID = "n_" + hex.EncodeToString(b)
	r.log.Info("fleet: generated node ID", "node_id", r.nodeID)
}

func (r *fleetReporter) isActive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

func (r *fleetReporter) status() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()

	s := map[string]any{
		"active":     r.active,
		"node_id":    r.nodeID,
		"endpoint":   r.endpoint,
		"last_sent":  r.lastSent,
		"queue_size": len(r.queue),
		"interval":   r.interval.String(),
	}

	if r.enrollment != nil {
		s["org_name"] = r.enrollment.OrgName
		s["team_name"] = r.enrollment.TeamName
		s["role"] = r.enrollment.Role
	}

	return s
}

func computeAdoptionTier(totalEvents int, acceptRate float64) int {
	switch {
	case totalEvents > 1000 && acceptRate > 0.5:
		return 3
	case totalEvents > 500 && acceptRate > 0.3:
		return 2
	case totalEvents > 100:
		return 1
	default:
		return 0
	}
}

func randInt63(max int64) int64 {
	if max <= 0 {
		return 0
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return 0
	}
	v := int64(b[0])<<56 | int64(b[1])<<48 | int64(b[2])<<40 | int64(b[3])<<32 |
		int64(b[4])<<24 | int64(b[5])<<16 | int64(b[6])<<8 | int64(b[7])
	if v < 0 {
		v = -v
	}
	return v % max
}
