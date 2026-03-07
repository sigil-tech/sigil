package actuator

import (
	"context"
	"testing"
	"time"
)

type mockStore struct {
	actions []string
}

func (m *mockStore) InsertAction(_ context.Context, actionID, description, executeCmd, undoCmd string, createdAt, expiresAt time.Time) error {
	m.actions = append(m.actions, actionID)
	return nil
}

type testActuator struct {
	name    string
	actions []Action
}

func (t *testActuator) Name() string { return t.name }
func (t *testActuator) Check(_ context.Context) ([]Action, error) {
	return t.actions, nil
}

func TestRegistry_Notify(t *testing.T) {
	store := &mockStore{}
	var notified []Action
	reg := New(store, func(a Action) {
		notified = append(notified, a)
	}, nil)

	action := Action{
		ID:          "test-1",
		Description: "Test action",
		ExpiresAt:   time.Now().Add(30 * time.Second),
	}
	reg.Notify(action)

	if len(store.actions) != 1 || store.actions[0] != "test-1" {
		t.Errorf("expected store to have action test-1; got %v", store.actions)
	}
	if len(notified) != 1 || notified[0].ID != "test-1" {
		t.Errorf("expected notify callback to receive action test-1; got %v", notified)
	}
}

func TestRegistry_Register(t *testing.T) {
	reg := New(nil, nil, nil)
	ta := &testActuator{name: "test"}
	reg.Register(ta)

	if len(reg.actuators) != 1 {
		t.Errorf("expected 1 registered actuator; got %d", len(reg.actuators))
	}
}
