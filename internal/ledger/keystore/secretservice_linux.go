//go:build linux

package keystore

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/godbus/dbus/v5"
)

// Constants of the freedesktop Secret Service interface the backend calls.
// https://specifications.freedesktop.org/secret-service/latest/
const (
	secretServiceDestination = "org.freedesktop.secrets"
	secretServicePath        = "/org/freedesktop/secrets"
	secretServiceInterface   = "org.freedesktop.Secret.Service"

	// Plain-text transport algorithm. The DBus session bus is already
	// scoped to the caller's UID; a process under a different user cannot
	// attach to it. Upgrading to the DH-IETF session-encryption algorithm
	// is a follow-up — it strengthens the threat model against an attacker
	// who can sniff DBus traffic within the same UID (e.g., via a
	// compromised helper process), but does not address the primary
	// concern that a compromised UID has already lost. Tracked for the
	// security-privacy agent review of Phase 2.
	secretServiceAlgorithmPlain = "plain"

	defaultSecretServiceLabel = "Sigil Ledger Signing Key"

	// itemAttributeApplication tags the secret-service entry so a future
	// recovery tool can locate it without hard-coding the service label.
	itemAttributeApplication = "application"
	itemAttributeService     = "service"
)

// secretServiceKeyStorage stores the ed25519 private key as raw bytes in
// the user's default Secret Service collection. The entry is labelled with
// cfg.SecretServiceLabel and tagged with (`application=sigild`,
// `service=tech.sigil.ledger`) attributes for SearchItems lookups.
type secretServiceKeyStorage struct {
	conn       *dbus.Conn
	service    dbus.BusObject
	sessionObj dbus.ObjectPath
	label      string
	attributes map[string]string

	logger *slog.Logger
	mu     sync.Mutex
}

func newSecretService(_ context.Context, cfg Config, logger *slog.Logger) (KeyStorage, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("%w: session bus connect: %v", ErrBackendUnavailable, err)
	}

	service := conn.Object(secretServiceDestination, secretServicePath)

	// Verify the Secret Service is actually registered on the bus. Some
	// headless Linux systems have DBus but no secrets daemon; detect and
	// fall through gracefully.
	call := service.Call(secretServiceInterface+".OpenSession", 0, secretServiceAlgorithmPlain, dbus.MakeVariant(""))
	if call.Err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: OpenSession: %v", ErrBackendUnavailable, call.Err)
	}

	var output dbus.Variant
	var sessionPath dbus.ObjectPath
	if err := call.Store(&output, &sessionPath); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: OpenSession decode: %v", ErrBackendUnavailable, err)
	}

	label := cfg.SecretServiceLabel
	if label == "" {
		label = defaultSecretServiceLabel
	}

	svcName := cfg.KeychainService
	if svcName == "" {
		svcName = defaultKeychainService
	}
	acctName := cfg.KeychainAccount
	if acctName == "" {
		acctName = defaultKeychainAccount
	}

	return &secretServiceKeyStorage{
		conn:       conn,
		service:    service,
		sessionObj: sessionPath,
		label:      label,
		attributes: map[string]string{
			itemAttributeApplication: acctName,
			itemAttributeService:     svcName,
		},
		logger: logger,
	}, nil
}

func (s *secretServiceKeyStorage) Backend() string { return "secret-service" }

// secretStruct mirrors the freedesktop.Secret.Item.Secret structure:
//
//	(oayays) = session, parameters, value, content_type.
type secretStruct struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

func (s *secretServiceKeyStorage) Load(_ context.Context) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// SearchItems(attributes) → (unlocked [o], locked [o]).
	var unlocked, locked []dbus.ObjectPath
	if err := s.service.Call(secretServiceInterface+".SearchItems", 0, s.attributes).
		Store(&unlocked, &locked); err != nil {
		return nil, nil, fmt.Errorf("keystore secret-service: SearchItems: %w", err)
	}

	items := append([]dbus.ObjectPath(nil), unlocked...)
	items = append(items, locked...)
	if len(items) == 0 {
		return nil, nil, ErrKeyNotFound
	}

	// Unlock any locked items first.
	if len(locked) > 0 {
		if err := s.unlock(locked); err != nil {
			return nil, nil, fmt.Errorf("keystore secret-service: Unlock: %w", err)
		}
	}

	// Read the first matching item. Multiple matches with the same
	// attributes would be a drift bug — we pick the oldest (first) and log.
	if len(items) > 1 {
		s.logger.Warn("ledger.key.secret-service.multiple_items",
			"count", len(items),
			"using", string(items[0]),
		)
	}
	item := s.conn.Object(secretServiceDestination, items[0])

	var sec secretStruct
	if err := item.Call("org.freedesktop.Secret.Item.GetSecret", 0, s.sessionObj).
		Store(&sec); err != nil {
		return nil, nil, fmt.Errorf("keystore secret-service: GetSecret: %w", err)
	}
	defer wipe(sec.Value)

	if len(sec.Value) != ed25519.PrivateKeySize {
		return nil, nil, fmt.Errorf("%w: secret-service value has %d bytes, expected %d", ErrCorruptKey, len(sec.Value), ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(append([]byte(nil), sec.Value...))
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, nil, fmt.Errorf("%w: derived public key has wrong type", ErrCorruptKey)
	}
	return priv, pub, nil
}

// unlock calls Secret.Service.Unlock for the provided item paths. If a
// prompt is returned we follow the DBus spec's Prompt.Prompt flow —
// the Secret Service daemon (gnome-keyring, kwallet5) will surface a GUI
// passphrase dialog. For a headless daemon on a Linux host without an
// unlocked keyring, Unlock returns an error and the caller falls through
// to age-file.
func (s *secretServiceKeyStorage) unlock(paths []dbus.ObjectPath) error {
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := s.service.Call(secretServiceInterface+".Unlock", 0, paths).
		Store(&unlocked, &prompt); err != nil {
		return err
	}
	if prompt == "/" {
		return nil
	}
	// Prompt handling: this is a blocking GUI prompt on desktop systems.
	// For the ledger's use case (daemon boot), a blocking prompt is
	// acceptable at start-up but should never happen after daemon is
	// running (the secret-service caches the unlock).
	prompObj := s.conn.Object(secretServiceDestination, prompt)
	return prompObj.Call("org.freedesktop.Secret.Prompt.Prompt", 0, "").Err
}

// Store writes (creating or replacing) the signing key as a secret-service
// item in the default collection. `replace = true` asks the daemon to
// overwrite any matching attribute set rather than creating a duplicate.
func (s *secretServiceKeyStorage) Store(_ context.Context, priv ed25519.PrivateKey, _ ed25519.PublicKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("keystore secret-service: private key has %d bytes, expected %d", len(priv), ed25519.PrivateKeySize)
	}

	const defaultCollection = "/org/freedesktop/secrets/aliases/default"
	collection := s.conn.Object(secretServiceDestination, defaultCollection)

	props := map[string]dbus.Variant{
		"org.freedesktop.Secret.Item.Label":      dbus.MakeVariant(s.label),
		"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(s.attributes),
	}

	secret := secretStruct{
		Session:     s.sessionObj,
		Parameters:  []byte{},
		Value:       append([]byte(nil), priv...),
		ContentType: "application/octet-stream",
	}
	defer wipe(secret.Value)

	var itemPath, prompt dbus.ObjectPath
	if err := collection.Call("org.freedesktop.Secret.Collection.CreateItem", 0, props, secret, true).
		Store(&itemPath, &prompt); err != nil {
		return fmt.Errorf("keystore secret-service: CreateItem: %w", err)
	}
	if prompt != "/" {
		if err := s.conn.Object(secretServiceDestination, prompt).
			Call("org.freedesktop.Secret.Prompt.Prompt", 0, "").Err; err != nil {
			return fmt.Errorf("keystore secret-service: Prompt after CreateItem: %w", err)
		}
	}

	return nil
}

// Ensure we use errors package even if the compiler's dead-code pass drops
// the darwin path.
var _ = errors.New
