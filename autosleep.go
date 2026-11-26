package main

import (
	"sync"
	"time"
)

type autosleepState string

const (
	autosleepAwake  autosleepState = "awake"
	autosleepAsleep autosleepState = "asleep"
)

// autosleepManager tracks last activity per container and decides sleep/wake transitions.
type autosleepManager struct {
	mutex     sync.Mutex
	idleLimit time.Duration
	lastSeen  map[string]time.Time
	states    map[string]autosleepState
}

func newAutosleepManager(idleLimit time.Duration) *autosleepManager {
	return &autosleepManager{idleLimit: idleLimit, lastSeen: map[string]time.Time{}, states: map[string]autosleepState{}}
}

// Touch records a request; it returns true when the container must be woken.
func (manager *autosleepManager) Touch(container string, now time.Time) bool {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	manager.lastSeen[container] = now
	wasAsleep := manager.states[container] == autosleepAsleep
	manager.states[container] = autosleepAwake
	return wasAsleep
}

// Sweep returns containers that exceeded the idle limit and marks them asleep.
func (manager *autosleepManager) Sweep(now time.Time) []string {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	var toSleep []string
	for container, seen := range manager.lastSeen {
		if manager.states[container] == autosleepAwake && now.Sub(seen) >= manager.idleLimit {
			manager.states[container] = autosleepAsleep
			toSleep = append(toSleep, container)
		}
	}
	return toSleep
}

func (manager *autosleepManager) State(container string) autosleepState {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	return manager.states[container]
}
