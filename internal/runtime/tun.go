package runtime

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/tundetect"
)

const defaultTunStack = "gVisor"

// TunStatus returns desired managed TUN intent plus live observation from mihomo when available.
func (m *Manager) TunStatus(ctx context.Context) (protocol.TunStatus, error) {
	if err := m.lockMaintenance(ctx); err != nil {
		return protocol.TunStatus{}, err
	}
	defer m.unlock()
	return m.buildTunStatus(ctx, ""), nil
}

// EnableTun persists managed TUN enable=true and applies it to the running core.
// When other TUN adapters are detected (routing conflict or loop risk), force
// must be true to proceed. Disable is never gated (see mutateTun).
func (m *Manager) EnableTun(ctx context.Context, op Operation, force bool) (protocol.TunStatus, error) {
	result, err := m.doOperation(ctx, "tun-enable:"+op.ID, func() (any, error) {
		return m.mutateTun(ctx, op, true, force)
	})
	if err != nil {
		return protocol.TunStatus{}, err
	}
	return result.(protocol.TunStatus), nil
}

// DisableTun persists managed TUN enable=false (block stays non-empty so subscription tun stays overridden).
func (m *Manager) DisableTun(ctx context.Context, op Operation) (protocol.TunStatus, error) {
	result, err := m.doOperation(ctx, "tun-disable:"+op.ID, func() (any, error) {
		return m.mutateTun(ctx, op, false, false)
	})
	if err != nil {
		return protocol.TunStatus{}, err
	}
	return result.(protocol.TunStatus), nil
}

func (m *Manager) mutateTun(ctx context.Context, op Operation, enable bool, force bool) (protocol.TunStatus, error) {
	if err := m.lockMutation(ctx); err != nil {
		return protocol.TunStatus{}, err
	}
	defer m.unlock()
	if err := ctx.Err(); err != nil {
		return protocol.TunStatus{}, err
	}
	if err := m.checkIfRevision(op.IfRevision); err != nil {
		return protocol.TunStatus{}, err
	}

	// Enable is gated when other TUN adapters are detected (signal A), mirroring the
	// system-proxy foreign gate. Disable is intentionally NOT gated: tearing down this
	// daemon's own mihomo tun block is non-destructive to other actors' TUN adapters.
	conflict := m.detectTunConflict(ctx)
	if err := ctx.Err(); err != nil {
		return protocol.TunStatus{}, err
	}
	if enable && !force {
		if conflict != nil && len(conflict.OtherTunInterfaces) > 0 {
			return protocol.TunStatus{}, protocol.APIError{
				Code:    protocol.CodeTunConflict,
				Message: "other TUN adapters detected; routing conflict or loop risk",
				Details: map[string]any{
					"other_tun_interfaces":   conflict.OtherTunInterfaces,
					"other_mihomo_processes": conflict.OtherMihomoProcesses,
				},
			}
		}
	}

	candidate, err := m.updateSettings(func(settings *config.Settings) error {
		settings.Tun = buildManagedTun(enable, settings.Tun)
		return nil
	})
	if err != nil {
		return protocol.TunStatus{}, err
	}
	if err := ctx.Err(); err != nil {
		return protocol.TunStatus{}, m.compensateTun(ctx, op, candidate, err, false)
	}
	nextTun := cloneTunMap(candidate.after.Tun)

	if applyErr := m.applyTun(ctx, nextTun); applyErr != nil {
		mapped := mapTunApplyError(applyErr)
		m.setTunLastError(tunErrorMessage(mapped))
		return protocol.TunStatus{}, m.compensateTun(ctx, op, candidate, mapped, true)
	}

	live, ok := false, false
	if m.controller != nil && ctx.Err() == nil {
		if configs, cfgErr := m.controller.Configs(ctx); cfgErr == nil {
			live, ok = liveTunEnable(configs)
		}
	}
	if !ok || live != enable {
		message := "TUN did not become live after apply"
		if !enable {
			message = "TUN did not become disabled after apply"
		}
		mapped := protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: message}
		m.setTunLastError(mapped.Message)
		return protocol.TunStatus{}, m.compensateTun(ctx, op, candidate, mapped, true)
	}
	liveEnable := &live

	m.setTunLastError("")

	_, err = m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{
		ID: op.ID, Source: op.Source, IfRevision: op.IfRevision,
	}, func(snapshot state.Snapshot) (state.Snapshot, error) {
		return snapshot, nil
	})
	if err != nil {
		// Apply already succeeded; keep desired state and surface the revision error.
		return protocol.TunStatus{}, err
	}
	return buildTunStatusFromObservation(candidate.after, m.store.Load().Revision, conflict, liveEnable, ""), nil
}

func (m *Manager) compensateTun(ctx context.Context, op Operation, candidate settingsCandidate, cause error, restoreLive bool) error {
	_, rollbackErr := m.restoreSettings(candidate.before)
	var liveRestoreErr error
	if restoreLive {
		liveRestoreErr = m.restoreTunLive(ctx, candidate.before.Tun, candidate.after.Tun)
	}
	if rollbackErr == nil && liveRestoreErr == nil {
		return cause
	}
	_, err := m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{
		ID: op.ID, Source: op.Source, IfRevision: op.IfRevision,
	}, func(snapshot state.Snapshot) (state.Snapshot, error) {
		degradedErr := m.enterMutationDegraded(&snapshot)
		return snapshot, degradedErr
	})
	return err
}

func (m *Manager) restoreTunLive(ctx context.Context, before, applied map[string]any) error {
	target := cloneTunMap(before)
	if len(target) == 0 {
		target = buildManagedTun(false, applied)
	}
	if err := m.applyTun(ctx, target); err != nil {
		return err
	}
	if m.controller == nil {
		return errors.New("TUN live restore is unconfirmed")
	}
	configs, err := m.controller.Configs(ctx)
	if err != nil {
		return err
	}
	live, ok := liveTunEnable(configs)
	if !ok || live != tunDesiredEnable(before) {
		return errors.New("TUN live restore is unconfirmed")
	}
	return nil
}

func (m *Manager) setTunLastError(message string) {
	m.settingsMu.Lock()
	m.tunLastError = message
	m.settingsMu.Unlock()
}

// applyTun prefers regenerating the runtime config (generator injects managed tun) and
// falls back to (or also uses) PATCH /configs for live apply.
func (m *Manager) applyTun(ctx context.Context, nextTun map[string]any) error {
	var regenerateErr, patchErr error
	regenerated := false

	if m.subscriptions != nil && m.runtimeConfig != "" && m.stagingDir != "" {
		catalog := m.subscriptions.Snapshot()
		candidate, err := m.prepareCatalogConfig(ctx, catalog)
		if err != nil {
			regenerateErr = err
		} else {
			defer os.Remove(candidate.path)
			if err := m.commitRuntimeConfig(ctx, candidate.content); err != nil {
				regenerateErr = err
			} else {
				regenerated = true
			}
		}
	}

	patched := false
	if m.controller != nil && nextTun != nil {
		if err := m.controller.PatchConfigs(ctx, map[string]any{"tun": nextTun}); err != nil {
			patchErr = err
		} else {
			patched = true
		}
	}

	if patchErr != nil {
		return patchErr
	}
	if regenerated || patched {
		return nil
	}
	if regenerateErr != nil {
		return regenerateErr
	}
	return protocol.APIError{
		Code:    protocol.CodeInvalidState,
		Message: "mihomo controller is unavailable",
	}
}

func (m *Manager) buildTunStatus(ctx context.Context, lastError string) protocol.TunStatus {
	settings := m.settingsSnapshot()
	m.settingsMu.Lock()
	if lastError == "" {
		lastError = m.tunLastError
	}
	m.settingsMu.Unlock()
	// Conflict evidence is always surfaced (even when only corroborating signal B is
	// present) so status/CLI/TUI can display it; the enable gate keys off
	// OtherTunInterfaces alone.
	conflict := m.detectTunConflict(ctx)

	var liveEnable *bool
	if m.controller != nil && ctx.Err() == nil {
		if configs, err := m.controller.Configs(ctx); err == nil {
			if live, ok := liveTunEnable(configs); ok {
				liveEnable = &live
			}
		}
	}
	return buildTunStatusFromObservation(settings, m.store.Load().Revision, conflict, liveEnable, lastError)
}

func buildTunStatusFromObservation(settings config.Settings, revision uint64, conflict *protocol.TunConflict, liveEnable *bool, lastError string) protocol.TunStatus {
	tun := cloneTunMap(settings.Tun)
	status := protocol.TunStatus{
		Schema:        "mihari/v1",
		Revision:      revision,
		DesiredEnable: tunDesiredEnable(tun),
		Managed:       len(tun) > 0,
		Stack:         tunStack(tun),
		LiveEnable:    liveEnable,
		Conflict:      conflict,
	}
	if lastError == "" && status.DesiredEnable && status.LiveEnable != nil && !*status.LiveEnable {
		lastError = "live TUN is off"
	}
	status.LastError = lastError
	return status
}

func buildManagedTun(enable bool, existing map[string]any) map[string]any {
	stack := defaultTunStack
	if s := tunStack(existing); s != "" {
		stack = s
	}
	return map[string]any{
		"enable": enable,
		"stack":  stack,
	}
}

func cloneTunMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func tunDesiredEnable(tun map[string]any) bool {
	if len(tun) == 0 {
		return false
	}
	enable, ok := tun["enable"].(bool)
	return ok && enable
}

func tunStack(tun map[string]any) string {
	if len(tun) == 0 {
		return ""
	}
	stack, _ := tun["stack"].(string)
	return stack
}

func liveTunEnable(configs map[string]any) (bool, bool) {
	raw, ok := configs["tun"]
	if !ok || raw == nil {
		return false, false
	}
	tun, ok := raw.(map[string]any)
	if !ok {
		return false, false
	}
	enable, ok := tun["enable"].(bool)
	if !ok {
		return false, false
	}
	return enable, true
}

func tunErrorMessage(err error) string {
	var api protocol.APIError
	if errors.As(err, &api) {
		return api.Message
	}
	return ""
}

func mapTunApplyError(err error) error {
	if err == nil {
		return nil
	}
	var api protocol.APIError
	if errors.As(err, &api) {
		if api.Code == protocol.CodePermissionDenied {
			return protocol.APIError{
				Code:    protocol.CodePermissionDenied,
				Message: "TUN requires elevated privileges; run Mihari as a service or from an elevated shell",
			}
		}
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"permission", "privilege", "access is denied", "operation not permitted",
		"not permitted", "elevat", "administrator",
	} {
		if strings.Contains(msg, needle) {
			return protocol.APIError{
				Code:    protocol.CodePermissionDenied,
				Message: "TUN requires elevated privileges; run Mihari as a service or from an elevated shell",
			}
		}
	}
	if errors.As(err, &api) {
		return api
	}
	return protocol.APIError{
		Code:    protocol.CodeUpstreamFailure,
		Message: "apply TUN configuration",
	}
}

// detectTunConflict returns classified conflict evidence, or nil when detection
// is unavailable or finds nothing. Detection failures are best-effort and never
// block enable: nil means "no evidence", so an opaque detection error cannot
// prevent a legitimate user action.
func (m *Manager) detectTunConflict(ctx context.Context) *protocol.TunConflict {
	if m.tunDetect == nil {
		return nil
	}
	detection, err := m.tunDetect.Detect(ctx)
	if err != nil {
		return nil
	}
	return tundetect.Classify(detection, m.selfFromLive(ctx))
}

// selfFromLive builds the Classify identity from the running core PID, the
// process holding the controller address, this daemon's PID, the managed
// core path, and live tun.enable / tun.device. Occupant lookup is
// best-effort. A missing controller or unreadable configs leave TunActive
// false so no adapter is subtracted.
func (m *Manager) selfFromLive(ctx context.Context) tundetect.Self {
	self := tundetect.Self{
		CorePID:    m.store.Load().Core.PID,
		DaemonPID:  os.Getpid(),
		BinaryPath: m.installRequest.BinaryPath,
	}
	if m.controller == nil || ctx.Err() != nil {
		return self
	}
	controllerAddr := m.settingsSnapshot().ControllerAddr
	if m.lookupOccupant != nil && controllerAddr != "" {
		if pid, ok := m.lookupOccupant(controllerAddr); ok && pid > 0 {
			self.OccupantPID = pid
		}
	}
	configs, err := m.controller.Configs(ctx)
	if err != nil {
		return self
	}
	if live, ok := liveTunEnable(configs); ok && live {
		self.TunActive = true
		self.TunName = liveTunDevice(configs)
	}
	return self
}

func liveTunDevice(configs map[string]any) string {
	raw, _ := configs["tun"].(map[string]any)
	device, _ := raw["device"].(string)
	return strings.TrimSpace(device)
}
