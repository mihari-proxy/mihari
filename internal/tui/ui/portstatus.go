package ui

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PortHoldKind is the user-visible ownership of a managed listen address.
type PortHoldKind uint8

const (
	// PortHoldUnknown means listen failed and no occupant could be identified.
	PortHoldUnknown PortHoldKind = iota
	// PortHoldAvailable means the address accepted a short-lived bind.
	PortHoldAvailable
	// PortHoldOwned means the occupant PID matches this instance's owner PID.
	PortHoldOwned
	// PortHoldOccupied means some other process holds the address.
	PortHoldOccupied
)

// PortHold is the classified result for one managed endpoint.
type PortHold struct {
	Kind    PortHoldKind
	Process string
	PID     int
}

// ClassifyPortHold decides ownership using listen result and occupant PID only.
// ownerPID is the PID that counts as self (core PID for mixed/controller, daemon
// PID for web). Process name is never used to decide Owned.
func ClassifyPortHold(listenFree bool, occupantPID int, occupantName string, ownerPID int) PortHold {
	if listenFree {
		return PortHold{Kind: PortHoldAvailable}
	}
	name := filepath.Base(strings.TrimSpace(occupantName))
	if occupantPID > 0 && ownerPID > 0 && occupantPID == ownerPID {
		return PortHold{Kind: PortHoldOwned, Process: name, PID: occupantPID}
	}
	if occupantPID > 0 {
		return PortHold{Kind: PortHoldOccupied, Process: name, PID: occupantPID}
	}
	return PortHold{Kind: PortHoldUnknown}
}

// PortHoldTone maps a hold to the shared status-shell tone.
func PortHoldTone(kind PortHoldKind) StatusTone {
	switch kind {
	case PortHoldOwned:
		return TonePositive
	case PortHoldOccupied:
		return ToneNegative
	default:
		return ToneNeutral
	}
}

// FormatPortHoldLabel is the row-trailing status text (no color).
func FormatPortHoldLabel(hold PortHold) string {
	switch hold.Kind {
	case PortHoldOwned:
		return PortOwned
	case PortHoldAvailable:
		return PortAvailable
	case PortHoldOccupied:
		name := strings.TrimSpace(hold.Process)
		if name != "" && hold.PID > 0 {
			return fmt.Sprintf(PortOccupiedByNamed, name, hold.PID)
		}
		if hold.PID > 0 {
			return fmt.Sprintf(PortOccupiedByPID, hold.PID)
		}
		return PortOccupiedByOtherApp
	default:
		return UnknownLabel
	}
}

// RenderPortHold paints StatusDot + colored label.
func RenderPortHold(theme Theme, hold PortHold) string {
	label := FormatPortHoldLabel(hold)
	tone := PortHoldTone(hold.Kind)
	return StatusDot(theme, tone, label)
}
