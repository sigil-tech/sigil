package launcherprofile

// BLOCKER (Phase 0b): the round-trip fixture is currently SYNTHETIC.
// When sigil-launcher-macos CI ships the real artefact, replace the
// synthetic file by running:
//
//	go generate ./internal/launcherprofile/
//
// The go:generate line below will be wired to fetch the pinned artefact
// once Phase 0b completes. The LAUNCHER_PROFILE_COMMIT placeholder must
// be replaced with the actual commit SHA from sigil-launcher-macos at
// that time.
//
//go:generate echo "Phase 0b pending: replace this with: gh release download --repo sigil-tech/sigil-launcher-macos --pattern launcher_profile_round_trip.json --dir internal/launcherprofile/testdata LAUNCHER_PROFILE_COMMIT"
