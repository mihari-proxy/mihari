package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

const (
	ValidationOK     = "ok"
	ValidationFailed = "failed"

	maxValidationReadyBytes = 16 << 10
	maxValidationPipeBytes  = 4 << 10
)

var (
	errMissingValidationPipe   = protocol.APIError{Code: protocol.CodeInvalidState, Message: "validation pipe lease is missing"}
	errValidationHandshake     = protocol.APIError{Code: protocol.CodeInvalidState, Message: "validation handshake rejected"}
	errValidationReady         = protocol.APIError{Code: protocol.CodeInvalidState, Message: "validation ready.json is not authoritative"}
	errValidationNotRoot       = protocol.APIError{Code: protocol.CodePermissionDenied, Message: "install validation requires root"}
	errValidationCanceled      = protocol.APIError{Code: protocol.CodeInvalidState, Message: "validation pipe closed"}
	errValidationFailed        = protocol.APIError{Code: protocol.CodeInvalidState, Message: "install validation failed"}
	errValidationChildRequired = protocol.APIError{Code: protocol.CodeInvalidState, Message: "validation child is required"}
)

// ProcessStartIdentity identifies a parent or validation daemon across PID reuse.
type ProcessStartIdentity struct {
	PID       int    `json:"pid"`
	BootID    string `json:"boot_id"`
	StartUnix int64  `json:"start_unix"`
	StartUsec uint32 `json:"start_usec,omitempty"`
}

// ValidationHandshake is the private pipe contract. Transaction and hashes come
// from the trusted journal, not from a user request.
type ValidationHandshake struct {
	JournalRoot    string
	TransactionID  string
	NonceHash      string
	CandidateHash  string
	ParentIdentity ProcessStartIdentity
	EUID           uint32
	LayoutIdentity string
}

// ValidationReady is B/transactions/<id>/ready.json. Ordinary /v1 status must
// not decode as this document.
type ValidationReady struct {
	TransactionID  string               `json:"transaction_id"`
	DaemonIdentity ProcessStartIdentity `json:"daemon_identity"`
	BinaryHash     string               `json:"binary_hash"`
	LayoutIdentity string               `json:"layout_identity"`
	Validation     string               `json:"validation"`
	SetupRequired  bool                 `json:"setup_required,omitempty"`
}

// ValidationLease is a private anonymous bidirectional pipe end.
type ValidationLease interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}

// DataEndpointLease is the installer's borrowed data/endpoint lock, released
// before the validation child starts.
type DataEndpointLease interface {
	Held() bool
	Release(context.Context) error
	WaitReleased(context.Context) error
}

// ValidationChild starts one no-business validation daemon.
type ValidationChild interface {
	Start(context.Context, ValidationStart) (ValidationSession, error)
}

// ValidationStart is the installer-owned launch request.
type ValidationStart struct {
	Lease     ValidationLease
	Child     ValidationLease
	Handshake ValidationHandshake
	Nonce     []byte
	Journal   InstallJournal
	Store     *InstallJournalStore
	DataLease DataEndpointLease
}

// ValidationSession is a started child until stop and lock release.
type ValidationSession interface {
	Identity() ProcessStartIdentity
	WaitReady(context.Context) error
	Stop(context.Context) error
	WaitLockRelease(context.Context) error
	Close() error
}

type validationPipeMessage struct {
	JournalRoot    string               `json:"journal_root,omitempty"`
	Nonce          string               `json:"nonce,omitempty"`
	NonceHash      string               `json:"nonce_hash,omitempty"`
	TransactionID  string               `json:"transaction_id,omitempty"`
	CandidateHash  string               `json:"candidate_hash,omitempty"`
	ParentIdentity ProcessStartIdentity `json:"parent_identity,omitempty"`
	LayoutIdentity string               `json:"layout_identity,omitempty"`
	OK             bool                 `json:"ok,omitempty"`
	Stop           bool                 `json:"stop,omitempty"`
}

func validationReadyPath(id string) string {
	return "transactions/" + id + "/ready.json"
}

func layoutIdentityOf(journal InstallJournal) string {
	return sha256Hex(strings.Join([]string{journal.Mode, journal.TargetPath, journal.EndpointPath, journal.CredentialPath, journal.InstallPath}, "\n"))
}

func newValidationNonce() (nonce []byte, hash string, err error) {
	nonce = make([]byte, 32)
	if _, err = rand.Read(nonce); err != nil {
		return nil, "", protocol.APIError{Code: protocol.CodeInternal, Message: "create validation nonce"}
	}
	return nonce, sha256HexBytes(nonce), nil
}

func nonceHashOf(nonce []byte) string {
	return sha256HexBytes(nonce)
}

// AcceptValidationHandshake verifies the inherited pipe and handshake against
// the journal intent. There is no environment-variable bypass.
func AcceptValidationHandshake(lease ValidationLease, expected ValidationHandshake, nonce []byte) error {
	if lease == nil {
		return errMissingValidationPipe
	}
	if expected.EUID != 0 {
		return errValidationNotRoot
	}
	msg, err := readValidationJSON(lease)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return errValidationCanceled
		}
		return err
	}
	return validateValidationHandshake(msg, expected, nonce)
}

func validateValidationHandshake(msg validationPipeMessage, expected ValidationHandshake, nonce []byte) error {
	gotNonce, err := hex.DecodeString(msg.Nonce)
	if err != nil || len(gotNonce) != 32 {
		return errValidationHandshake
	}
	gotHash := nonceHashOf(gotNonce)
	if gotHash != expected.NonceHash || msg.NonceHash != expected.NonceHash {
		return errValidationHandshake
	}
	if len(nonce) > 0 && !bytes.Equal(nonce, gotNonce) {
		return errValidationHandshake
	}
	if msg.TransactionID != expected.TransactionID || msg.CandidateHash != expected.CandidateHash {
		return errValidationHandshake
	}
	if msg.ParentIdentity != expected.ParentIdentity {
		return errValidationHandshake
	}
	if expected.LayoutIdentity != "" && msg.LayoutIdentity != expected.LayoutIdentity {
		return errValidationHandshake
	}
	return nil
}

// DecodeValidationReady strictly decodes ready.json. /v1 status documents are rejected.
func DecodeValidationReady(raw []byte) (ValidationReady, error) {
	var ready ValidationReady
	keys, err := decodeStrictJSON(bytes.NewReader(raw), maxValidationReadyBytes, &ready)
	if err != nil {
		return ValidationReady{}, errValidationReady
	}
	for _, key := range []string{"schema", "health", "protocol_version", "capabilities", "revision", "daemon_version"} {
		if keys[key] {
			return ValidationReady{}, errValidationReady
		}
	}
	if !validTransactionID(ready.TransactionID) || !validSHA256(ready.BinaryHash) || ready.LayoutIdentity == "" {
		return ValidationReady{}, errValidationReady
	}
	if ready.Validation != ValidationOK && ready.Validation != ValidationFailed {
		return ValidationReady{}, errValidationReady
	}
	if ready.DaemonIdentity.PID <= 0 || ready.DaemonIdentity.BootID == "" {
		return ValidationReady{}, errValidationReady
	}
	return ready, nil
}

// MatchValidationReady authorizes activation only from a matching ready.json.
func MatchValidationReady(journal InstallJournal, ready ValidationReady) error {
	if ready.TransactionID != journal.TransactionID {
		return errValidationReady
	}
	if ready.BinaryHash != journal.CandidateHash {
		return errValidationReady
	}
	if ready.LayoutIdentity != layoutIdentityOf(journal) {
		return errValidationReady
	}
	if ready.DaemonIdentity.PID <= 0 || ready.DaemonIdentity.BootID == "" {
		return errValidationReady
	}
	if ready.Validation != ValidationOK {
		return errValidationFailed
	}
	return nil
}

// SameProcessStart reports whether recorded still names the live process.
// Cross-boot identities never match, so a reused PID is not reaped.
func SameProcessStart(recorded, live ProcessStartIdentity) bool {
	if recorded.PID <= 0 || recorded.BootID == "" || live.BootID == "" {
		return false
	}
	if recorded.BootID != live.BootID {
		return false
	}
	return recorded.PID == live.PID && recorded.StartUnix == live.StartUnix && recorded.StartUsec == live.StartUsec
}

func (x *InstallTransaction) runValidation(ctx context.Context) (resultErr error) {
	defer func() { resultErr = errors.Join(resultErr, x.stopValidation(context.WithoutCancel(ctx))) }()
	nonce, hash, err := newValidationNonce()
	if err != nil {
		return err
	}
	x.validationNonce = nonce
	start := JournalAction{
		Kind:         JournalActionValidationStart,
		TargetRole:   JournalRoleValidation,
		OldState:     "idle",
		NewState:     "running",
		CandidateRef: hash,
	}
	if err := x.step(ctx, start, func(ctx context.Context) error {
		if err := x.startAndWaitValidation(ctx, nonce, hash); err != nil {
			return err
		}
		if x.Effects != nil {
			return x.Effects.Apply(ctx, start)
		}
		return nil
	}); err != nil {
		return err
	}
	stop := JournalAction{
		Kind:       JournalActionValidationStop,
		TargetRole: JournalRoleValidation,
		OldState:   "running",
		NewState:   "idle",
	}
	return x.step(ctx, stop, func(ctx context.Context) error {
		if err := x.stopValidation(ctx); err != nil {
			return err
		}
		if x.Effects != nil {
			return x.Effects.Apply(ctx, stop)
		}
		return nil
	})
}

func (x *InstallTransaction) startAndWaitValidation(ctx context.Context, nonce []byte, hash string) (resultErr error) {
	launch := validationLaunch{TransactionID: x.journal.TransactionID, NonceHash: hash, Parent: x.ParentIdentity, BinaryHash: x.journal.CandidateHash, LayoutIdentity: layoutIdentityOf(x.journal)}
	if !validProcessStart(launch.Parent) {
		return errValidationHandshake
	}
	if err := x.Store.saveValidationLaunch(ctx, launch); err != nil {
		return err
	}
	if x.DataLease != nil {
		if err := x.DataLease.Release(ctx); err != nil {
			return err
		}
	}
	parent, child := x.validationPipes()
	if child == nil {
		return errMissingValidationPipe
	}
	hs := ValidationHandshake{
		JournalRoot:    x.Store.validationRoot(),
		TransactionID:  x.journal.TransactionID,
		NonceHash:      hash,
		CandidateHash:  x.journal.CandidateHash,
		ParentIdentity: x.ParentIdentity,
		EUID:           x.EUID,
		LayoutIdentity: layoutIdentityOf(x.journal),
	}
	if x.Validation == nil {
		closeValidationLease(parent)
		closeValidationLease(child)
		return errValidationChildRequired
	}
	sess, err := x.Validation.Start(ctx, ValidationStart{
		Lease:     parent,
		Child:     child,
		Handshake: hs,
		Nonce:     nonce,
		Journal:   x.journal,
		Store:     x.Store,
		DataLease: x.DataLease,
	})
	if err != nil {
		closeValidationLease(parent)
		closeValidationLease(child)
		return err
	}
	x.validation = sess
	launch.Child = sess.Identity()
	if !validProcessStart(launch.Child) {
		return errValidationHandshake
	}
	if err := x.Store.saveValidationLaunch(ctx, launch); err != nil {
		return err
	}
	writerDone := make(chan error, 1)
	go func() {
		err := writeValidationJSON(parent, validationPipeMessage{
			JournalRoot:    hs.JournalRoot,
			Nonce:          hex.EncodeToString(nonce),
			NonceHash:      hash,
			TransactionID:  hs.TransactionID,
			CandidateHash:  hs.CandidateHash,
			ParentIdentity: hs.ParentIdentity,
			LayoutIdentity: hs.LayoutIdentity,
		})
		if err != nil {
			closeValidationLease(parent)
			closeValidationLease(child)
		}
		writerDone <- err
	}()
	defer func() {
		if resultErr != nil {
			closeValidationLease(parent)
			closeValidationLease(child)
		}
		resultErr = errors.Join(resultErr, <-writerDone)
	}()
	if err := sess.WaitReady(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if x.Store == nil || x.Store.files == nil {
		return errValidationReady
	}
	raw, err := x.Store.files.read(ctx, validationReadyPath(x.journal.TransactionID), maxValidationReadyBytes)
	if err != nil {
		return errValidationReady
	}
	ready, err := DecodeValidationReady(raw)
	if err != nil {
		return err
	}
	if err := MatchValidationReady(x.journal, ready); err != nil {
		return err
	}
	if !SameProcessStart(sess.Identity(), ready.DaemonIdentity) {
		return errValidationReady
	}
	x.validationReady = ready
	return nil
}

func (x *InstallTransaction) stopValidation(ctx context.Context) error {
	if x.validation == nil {
		return nil
	}
	sess := x.validation
	x.validation = nil
	// Cleanup ignores caller cancellation: returning transfers install-lock ownership.
	ctx = context.WithoutCancel(ctx)
	stopErr := sess.Stop(ctx)
	closeErr := sess.Close()
	waitErr := sess.WaitLockRelease(ctx)
	return errors.Join(stopErr, closeErr, waitErr)
}

func (x *InstallTransaction) validationPipes() (parent, child ValidationLease) {
	if x.NewPipe != nil {
		return x.NewPipe()
	}
	return newMemoryValidationPipes()
}

func closeValidationLease(lease ValidationLease) {
	if lease != nil {
		_ = lease.Close()
	}
}

func writeValidationJSON(w io.Writer, msg validationPipeMessage) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return errValidationHandshake
	}
	if len(raw)+1 > maxValidationPipeBytes {
		return errValidationHandshake
	}
	_, err = w.Write(append(raw, '\n'))
	return err
}

func readValidationJSON(r io.Reader) (validationPipeMessage, error) {
	var buf bytes.Buffer
	// Read exactly one frame. A stream read may contain a later stop frame.
	var one [1]byte
	for buf.Len() <= maxValidationPipeBytes {
		n, err := r.Read(one[:])
		if n > 0 {
			if one[0] == '\n' {
				var msg validationPipeMessage
				if _, err := decodeStrictJSON(bytes.NewReader(buf.Bytes()), maxValidationPipeBytes, &msg); err != nil {
					return msg, errValidationHandshake
				}
				return msg, nil
			}
			buf.WriteByte(one[0])
		}
		if err != nil {
			return validationPipeMessage{}, errValidationCanceled
		}
	}
	return validationPipeMessage{}, errValidationHandshake
}

type memoryPipe struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (p *memoryPipe) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *memoryPipe) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *memoryPipe) Close() error                { return errors.Join(p.r.Close(), p.w.Close()) }

func newMemoryValidationPipes() (parent, child ValidationLease) {
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()
	return &memoryPipe{r: r2, w: w1}, &memoryPipe{r: r1, w: w2}
}
