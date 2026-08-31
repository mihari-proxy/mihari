package connections

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type Client interface {
	CloseConnection(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
	CloseAllConnections(context.Context, protocol.MutationRequest) (protocol.MutationResult, error)
	UpdateTUIPreferences(context.Context, protocol.UpdateTUIPreferencesRequest) (protocol.TUIPreferences, error)
	LookupGeoIP(context.Context, protocol.GeoIPLookupRequest) (protocol.GeoIPLookupResult, error)
}

type datasetKind uint8

const (
	datasetActive datasetKind = iota
	datasetClosed
)

type focusKind uint8

const (
	focusControl focusKind = iota
	focusSearch
	focusHeader
	focusRow
)

type pageFocus struct {
	kind  focusKind
	rowID string
}

type sortDirection uint8

const (
	sortDefault sortDirection = iota
	sortDescending
	sortAscending
)

// allColumnIDs is the positional order of connection columns, which is also
// the priority order: position is importance, so narrower widths drop from
// the tail (design §2.4). Checked order, table order and drop order are one.
var allColumnIDs = []string{"host", "traffic", "network", "rule", "start", "process", "chain", "source", "destination", "upload", "download"}

type Model struct {
	client         Client
	newOperationID func() string
	history        *History
	dataset        datasetKind
	focus          pageFocus
	controlIndex   int
	headerIndex    int
	source         string
	query          string
	queryCursor    int
	searching      bool
	paused         bool
	pending        *protocol.ConnectionList
	pendingAt      time.Time
	columns        []string
	preferenceRev  uint64
	sortColumn     string
	sortDirection  sortDirection
	detail         *Detail
	columnsOpen    bool
	columnCursor   int
	columnDraft    map[string]bool
	closing        map[string]bool
	contentFocused bool
	width          int
	height         int
	theme          ui.Theme
}

type closeResultMsg struct {
	id  string
	err error
}

// Err implements the shell's action-outcome contract so connection closes are
// classified Succeeded/Failed in the Recent operations ledger.
func (m closeResultMsg) Err() error { return m.err }

var _ interface{ Err() error } = closeResultMsg{}

type preferencesResultMsg struct {
	preferences protocol.TUIPreferences
	err         error
}

type geoIPResultMsg struct {
	connectionID string
	records      []protocol.GeoIPRecord
	err          error
}

func New(client Client, newOperationID func() string) *Model {
	if newOperationID == nil {
		newOperationID = defaultConnectionOperationID
	}
	return &Model{
		client: client, newOperationID: newOperationID, history: NewHistory(500),
		focus: pageFocus{kind: focusControl}, source: allSources,
		// Default 5 columns = the 5 highest-priority slots in allColumnIDs order,
		// so the checked set matches what a 100-column terminal actually shows.
		columns: []string{"host", "traffic", "network", "rule", "start"},
		closing: make(map[string]bool), theme: ui.DefaultTheme(),
	}
}

func (m *Model) ID() ui.PageID { return ui.PageConnections }

// SetContentFocused reports whether the root shell has given keyboard focus to this page.
func (m *Model) SetContentFocused(focused bool) { m.contentFocused = focused }

func (m *Model) HelpMode() string {
	switch {
	case m.detail != nil:
		return ui.ModeDetail
	case m.columnsOpen:
		return ui.ModeColumns
	case m.searching:
		return ui.ModeSearch
	default:
		return ""
	}
}

// FooterHints returns contextual shortcuts for the root shell footer.
func (m *Model) FooterHints() string {
	return ui.RenderFooter(m.ID(), m.HelpMode(), ui.FooterOpt{})
}

func (m *Model) SetSize(width, height int) { m.width, m.height = width, height }

func (m *Model) FocusFirst() { m.focus = pageFocus{kind: focusControl} }

// ResetSession discards connection state that only exists for the current TUI stream session.
func (m *Model) ResetSession() {
	m.history.Reset()
	m.pending = nil
	m.detail = nil
	m.focus = pageFocus{kind: focusControl}
}

func (m *Model) SetPreferences(preferences protocol.TUIPreferences) {
	if len(preferences.ConnectionsColumns) > 0 {
		m.columns = append([]string(nil), preferences.ConnectionsColumns...)
	}
	m.preferenceRev = preferences.Revision
	// headerIndex addresses the kept-column set, so clamp to its size.
	m.headerIndex = min(m.headerIndex, max(0, m.headerColumnCount()-1))
}

func (m *Model) Observe(snapshot protocol.ConnectionList, observedAt time.Time) {
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	if m.paused {
		copy := snapshot
		copy.Connections = cloneConnections(snapshot.Connections)
		m.pending, m.pendingAt = &copy, observedAt
		return
	}
	m.history.Observe(snapshot.Connections, observedAt)
	m.reconcile()
}

func (m *Model) Update(message tea.Msg) (ui.Page, tea.Cmd) {
	switch typed := message.(type) {
	case closeResultMsg:
		delete(m.closing, typed.id)
		return m, nil
	case preferencesResultMsg:
		if typed.err == nil {
			m.SetPreferences(typed.preferences)
		}
		m.columnsOpen = false
		return m, nil
	case geoIPResultMsg:
		if m.detail != nil && m.detail.connection.ID == typed.connectionID {
			m.detail.SetGeoIP(typed.records, typed.err)
		}
		return m, nil
	}
	if m.detail != nil {
		if m.detail.Update(message) {
			m.detail = nil
		}
		return m, nil
	}
	if m.columnsOpen {
		return m.updateColumns(message)
	}
	if m.searching {
		return m.updateSearch(message)
	}
	// Search bar focus is a character-input surface: typing starts filter without Enter.
	// Page shortcuts (p, x, /…) are disabled here so printable keys edit the query.
	if m.focus.kind == focusSearch {
		return m.updateSearchFocus(message)
	}
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if key.String() == "esc" {
		// Esc returns to the rail; nested modes (search/detail/columns) are handled above.
		return m, func() tea.Msg { return ui.FocusRailMsg{} }
	}
	if key.String() == "/" {
		return m, m.startSearch()
	}
	if key.String() == "p" {
		m.setPaused(!m.paused)
		return m, nil
	}
	if key.String() == "ctrl+x" && m.dataset == datasetActive {
		return m, func() tea.Msg {
			return ui.ActionIntentMsg{
				Action: ui.ActionCloseAllConnections, Page: ui.PageConnections, Capability: protocol.CapabilityConnections, Key: "connections:close-all",
				Title: ui.CloseAllConnectionsTitle, Object: ui.AllActiveConnections,
				Impact: ui.CloseAllImpact, Rollback: ui.CloseAllRollback, Execute: m.closeAllConnections(),
			}
		}
	}
	switch m.focus.kind {
	case focusControl:
		return m.updateControl(key)
	case focusHeader:
		return m.updateHeader(key)
	case focusRow:
		return m.updateRow(key)
	default:
		return m, nil
	}
}

func (m *Model) updateControl(key tea.KeyPressMsg) (ui.Page, tea.Cmd) {
	switch key.String() {
	case "left":
		m.controlIndex = max(0, m.controlIndex-1)
	case "right", "tab":
		m.controlIndex = min(3, m.controlIndex+1)
	case "down":
		return m, m.startSearch()
	case "enter":
		switch m.controlIndex {
		case 0:
			m.dataset = datasetKind(1 - m.dataset)
			m.reconcile()
		case 1:
			options := sourceOptions(m.datasetRows())
			m.source = options[(slices.Index(options, m.source)+1)%len(options)]
			m.reconcile()
		case 2:
			m.openColumns()
		case 3:
			m.setPaused(!m.paused)
		}
	}
	return m, nil
}

// updateSearchFocus handles keys while the search bar is focused but not yet in
// active character-input mode (after Esc). Typing re-enters input mode without Enter.
func (m *Model) updateSearchFocus(message tea.Msg) (ui.Page, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "up":
			m.focus = pageFocus{kind: focusControl}
			return m, nil
		case "down":
			m.focus = pageFocus{kind: focusHeader}
			return m, nil
		}
	}
	if ui.IsTextEditMsg(message) {
		enter := m.startSearch()
		page, edit := m.updateSearch(message)
		return page, tea.Batch(enter, edit)
	}
	// Non-text keys (enter, page shortcuts) are ignored while search is focused.
	return m, nil
}

func (m *Model) updateHeader(key tea.KeyPressMsg) (ui.Page, tea.Cmd) {
	switch key.String() {
	case "left":
		m.headerIndex = max(0, m.headerIndex-1)
	case "right":
		// headerIndex walks the kept columns (columns that fit on screen).
		m.headerIndex = min(m.headerColumnCount()-1, m.headerIndex+1)
	case "up":
		return m, m.startSearch()
	case "down":
		if rows := m.visibleRows(); len(rows) > 0 {
			m.focus = pageFocus{kind: focusRow, rowID: rows[0].ID}
		}
	case "enter":
		m.cycleSort()
	}
	return m, nil
}

func (m *Model) updateRow(key tea.KeyPressMsg) (ui.Page, tea.Cmd) {
	rows := m.visibleRows()
	index := rowIndex(rows, m.focus.rowID)
	switch key.String() {
	case "up":
		if index <= 0 {
			m.focus = pageFocus{kind: focusHeader}
		} else {
			m.focus.rowID = rows[index-1].ID
		}
	case "down":
		if index >= 0 && index+1 < len(rows) {
			m.focus.rowID = rows[index+1].ID
		}
	case "enter":
		if index >= 0 {
			return m, m.openDetail(rows[index])
		}
	case "x":
		if m.dataset == datasetActive && index >= 0 {
			return m, m.closeConnection(rows[index].ID)
		}
	}
	return m, nil
}

func (m *Model) openDetail(connection protocol.Connection) tea.Cmd {
	m.detail = NewDetail(connection, m.dataset == datasetClosed)
	addresses := publicConnectionAddresses(connection)
	if m.client == nil || len(addresses) == 0 {
		m.detail.SetGeoIP(nil, nil)
		return nil
	}
	connectionID := connection.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, err := m.client.LookupGeoIP(ctx, protocol.GeoIPLookupRequest{Addresses: addresses})
		return geoIPResultMsg{connectionID: connectionID, records: result.Records, err: err}
	}
}

func publicConnectionAddresses(connection protocol.Connection) []string {
	candidates := []string{connection.Metadata.DestinationIP}
	remote := connection.Metadata.RemoteDestination
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	candidates = append(candidates, strings.Trim(remote, "[]"))
	result := make([]string, 0, len(candidates))
	seen := make(map[netip.Addr]struct{}, len(candidates))
	for _, raw := range candidates {
		address, err := netip.ParseAddr(raw)
		if err != nil {
			continue
		}
		address = address.Unmap()
		if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsMulticast() || address.IsUnspecified() {
			continue
		}
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		result = append(result, address.String())
	}
	return result
}

func (m *Model) updateSearch(message tea.Msg) (ui.Page, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			m.searching = false
			m.reconcile()
			return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
		case "up":
			m.searching = false
			m.focus = pageFocus{kind: focusControl}
			m.reconcile()
			return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
		case "down":
			m.searching = false
			m.focus = pageFocus{kind: focusHeader}
			m.reconcile()
			return m, func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
		}
	}
	value, cursor, handled, command := ui.EditTextField(m.query, m.queryCursor, message, 256)
	if handled {
		m.query = value
		m.queryCursor = cursor
		m.reconcile()
		return m, command
	}
	// Page shortcuts disabled while typing (left/right already handled as cursor).
	return m, nil
}

func (m *Model) startSearch() tea.Cmd {
	m.searching = true
	m.focus = pageFocus{kind: focusSearch}
	m.queryCursor = utf8.RuneCountInString(m.query)
	return func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputText} }
}

func (m *Model) openColumns() {
	m.columnsOpen = true
	m.columnCursor = 0
	m.columnDraft = make(map[string]bool, len(m.columns))
	for _, column := range m.columns {
		m.columnDraft[column] = true
	}
}

func (m *Model) updateColumns(message tea.Msg) (ui.Page, tea.Cmd) {
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "esc":
		m.columnsOpen = false
	case "up":
		m.columnCursor = max(0, m.columnCursor-1)
	case "down", "tab":
		m.columnCursor = min(len(allColumnIDs)-1, m.columnCursor+1)
	case "space":
		id := allColumnIDs[m.columnCursor]
		if !m.columnDraft[id] || len(m.selectedDraftColumns()) > 1 {
			m.columnDraft[id] = !m.columnDraft[id]
		}
	case "enter":
		return m, m.saveColumns()
	}
	return m, nil
}

func (m *Model) saveColumns() tea.Cmd {
	if m.client == nil {
		m.columnsOpen = false
		return nil
	}
	columns, operationID, revision := m.selectedDraftColumns(), m.newOperationID(), m.preferenceRev
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		preferences, err := m.client.UpdateTUIPreferences(ctx, protocol.UpdateTUIPreferencesRequest{
			OperationID: operationID, IfRevision: &revision, ConnectionsColumns: columns,
		})
		return preferencesResultMsg{preferences: preferences, err: err}
	}
}

func (m *Model) selectedDraftColumns() []string {
	result := make([]string, 0, len(m.columnDraft))
	for _, column := range allColumnIDs {
		if m.columnDraft[column] {
			result = append(result, column)
		}
	}
	return result
}

func (m *Model) setPaused(paused bool) {
	m.paused = paused
	if !paused && m.pending != nil {
		pending, observedAt := *m.pending, m.pendingAt
		m.pending = nil
		m.Observe(pending, observedAt)
	}
}

func (m *Model) cycleSort() {
	cols, _ := m.keptConnectionColumns()
	if len(cols) == 0 {
		return
	}
	// headerIndex addresses the kept set; map back to the checked column ID.
	column := cols[min(m.headerIndex, len(cols)-1)].ID
	if m.sortColumn != column {
		m.sortColumn, m.sortDirection = column, sortDescending
	} else {
		m.sortDirection = (m.sortDirection + 1) % 3
	}
	m.reconcile()
}

func (m *Model) datasetRows() []protocol.Connection {
	if m.dataset == datasetClosed {
		return m.history.Closed()
	}
	return m.history.Active()
}

func (m *Model) visibleRows() []protocol.Connection {
	rows := m.datasetRows()
	filtered := make([]protocol.Connection, 0, len(rows))
	for _, connection := range rows {
		if matchesConnection(connection, m.query, m.source, m.columns) {
			filtered = append(filtered, connection)
		}
	}
	if m.sortDirection != sortDefault && m.sortColumn != "" {
		slices.SortStableFunc(filtered, func(first, second protocol.Connection) int {
			comparison := strings.Compare(columnValue(first, m.sortColumn), columnValue(second, m.sortColumn))
			if m.sortColumn == "upload" || m.sortColumn == "download" || m.sortColumn == "traffic" {
				comparison = compareInt(columnNumber(first, m.sortColumn), columnNumber(second, m.sortColumn))
			}
			if m.sortDirection == sortDescending {
				return -comparison
			}
			return comparison
		})
	}
	return filtered
}

func (m *Model) reconcile() {
	m.source = validSource(m.source, sourceOptions(m.datasetRows()))
	rows := m.visibleRows()
	if m.focus.kind == focusRow && rowIndex(rows, m.focus.rowID) < 0 {
		if len(rows) == 0 {
			m.focus = pageFocus{kind: focusHeader}
		} else {
			m.focus.rowID = rows[0].ID
		}
	}
	if m.detail != nil {
		for _, connection := range append(m.history.Active(), m.history.Closed()...) {
			if connection.ID == m.detail.connection.ID {
				m.detail.Refresh(connection, !connection.ClosedAt.IsZero())
				break
			}
		}
	}
}

func (m *Model) closeConnection(id string) tea.Cmd {
	if m.client == nil || id == "" || m.closing[id] {
		return nil
	}
	m.closing[id] = true
	operationID := m.newOperationID()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := m.client.CloseConnection(ctx, id, protocol.MutationRequest{OperationID: operationID})
		return closeResultMsg{id: id, err: err}
	}
}

func (m *Model) closeAllConnections() tea.Cmd {
	if m.client == nil {
		return nil
	}
	operationID := m.newOperationID()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := m.client.CloseAllConnections(ctx, protocol.MutationRequest{OperationID: operationID})
		return closeResultMsg{err: err}
	}
}

func rowIndex(rows []protocol.Connection, id string) int {
	for index, row := range rows {
		if row.ID == id {
			return index
		}
	}
	return -1
}

func compareInt(first, second int64) int {
	if first < second {
		return -1
	}
	if first > second {
		return 1
	}
	return 0
}

var connectionOperationID atomic.Uint64

func defaultConnectionOperationID() string {
	return fmt.Sprintf("tui-connection-%d", connectionOperationID.Add(1))
}
