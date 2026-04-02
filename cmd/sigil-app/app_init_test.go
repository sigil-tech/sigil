package main

import (
	"testing"
)

func TestDetectEnvironment(t *testing.T) {
	t.Parallel()

	app := NewApp()
	env := app.DetectEnvironment()

	// We can't assert specific tools exist, but the function should not panic
	// and should return a valid struct.
	if env.IDEs == nil {
		env.IDEs = []string{} // normalize
	}
	if env.Tools == nil {
		env.Tools = []string{}
	}
	if env.Plugins == nil {
		env.Plugins = []string{}
	}

	// On any dev machine, git should be detectable.
	found := false
	for _, tool := range env.Tools {
		if tool == "git" {
			found = true
			break
		}
	}
	if !found {
		t.Log("git not detected (expected on most dev machines)")
	}
}
