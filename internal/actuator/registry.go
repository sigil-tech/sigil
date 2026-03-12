package actuator

import (
	"context"
	"log/slog"
	"os/exec"
	"time"
)

// RunCmd executes a shell command. The default uses sh -c via os/exec.
// It can be replaced in Registry for testing.
type RunCmd func(ctx context.Context, cmd string) error

// defaultRunCmd executes cmd via "sh -c".
func defaultRunCmd(ctx context.Context, cmd string) error {
	return exec.CommandContext(ctx, "sh", "-c", cmd).Run()
}

// StoreWriter is the subset of store.Store that the registry needs for
// persisting actions. Defined as an interface to avoid an import cycle
// (store imports event; actuator is a sibling package).
type StoreWriter interface {
	InsertAction(ctx context.Context, actionID, description, executeCmd, undoCmd string, createdAt, expiresAt time.Time) error
}

// Registry manages registered actuators and polls them periodically.
type Registry struct {
	actuators []Actuator
	notify    func(Action) // called when an action is taken; wired to socket.Notify in main
	store     StoreWriter
	runCmd    RunCmd
	log       *slog.Logger
}

// New creates a new actuator Registry.
func New(s StoreWriter, notify func(Action), log *slog.Logger) *Registry {
	return &Registry{
		store:  s,
		notify: notify,
		runCmd: defaultRunCmd,
		log:    log,
	}
}

// SetRunCmd overrides the command executor used by poll. Intended for testing.
func (r *Registry) SetRunCmd(fn RunCmd) {
	r.runCmd = fn
}

// Register adds an actuator to the registry.
func (r *Registry) Register(a Actuator) {
	r.actuators = append(r.actuators, a)
}

// Run polls all registered actuators every 30 seconds until ctx is cancelled.
func (r *Registry) Run(ctx context.Context) {
	r.poll(ctx)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.poll(ctx)
		}
	}
}

func (r *Registry) poll(ctx context.Context) {
	for _, a := range r.actuators {
		actions, err := a.Check(ctx)
		if err != nil {
			r.log.Warn("actuator check failed", "actuator", a.Name(), "err", err)
			continue
		}
		for _, action := range actions {
			// Execute the command if set.
			if action.ExecuteCmd != "" {
				if err := r.runCmd(ctx, action.ExecuteCmd); err != nil {
					r.log.Warn("actuator execute failed",
						"actuator", a.Name(),
						"action", action.ID,
						"cmd", action.ExecuteCmd,
						"err", err)
					continue
				}
			}

			// Persist to the action log.
			if r.store != nil {
				if err := r.store.InsertAction(ctx,
					action.ID, action.Description,
					action.ExecuteCmd, action.UndoCmd,
					time.Now(), action.ExpiresAt,
				); err != nil {
					r.log.Warn("actuator store failed", "action", action.ID, "err", err)
				}
			}

			// Notify subscribers.
			if r.notify != nil {
				r.notify(action)
			}

			r.log.Info("actuator action taken",
				"actuator", a.Name(),
				"action", action.ID,
				"description", action.Description)
		}
	}
}

// Notify exposes the notify callback so event-driven actuators (like
// BuildSplitActuator) can push actions without going through the poll loop.
func (r *Registry) Notify(action Action) {
	if r.store != nil {
		ctx := context.Background()
		_ = r.store.InsertAction(ctx,
			action.ID, action.Description,
			action.ExecuteCmd, action.UndoCmd,
			time.Now(), action.ExpiresAt,
		)
	}
	if r.notify != nil {
		r.notify(action)
	}
}
