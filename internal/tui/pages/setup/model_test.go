package setup

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type fakeClient struct {
	status             protocol.OnboardingStatus
	installCalls       int
	installRequest     protocol.MutationRequest
	addCalls           int
	geoIPCalls         int
	geoIPRequest       protocol.MutationRequest
	updateCalls        int
	update             protocol.OnboardingUpdateRequest
	onboardingCalls    int
	installErr         error
	coreStatus         protocol.CoreStatus
	coreErr            error
	coreCalls          int
	geoIPStatus        protocol.GeoIPStatus
	geoIPErr           error
	geoIPStatusCalls   int
	installResult      protocol.CoreInstallResult
	subscriptionResult protocol.SubscriptionResult
	geoIPUpdateResult  protocol.GeoIPUpdateResult
	serviceStatus      protocol.ServiceStatus
	serviceErr         error
	serviceCalls       int
}

func (f *fakeClient) Onboarding(context.Context) (protocol.OnboardingStatus, error) {
	f.onboardingCalls++
	return f.status, nil
}
func (f *fakeClient) InstallCore(ctx context.Context, request protocol.MutationRequest) (protocol.CoreInstallResult, error) {
	f.installCalls++
	f.installRequest = request
	if f.installErr != nil {
		return protocol.CoreInstallResult{}, f.installErr
	}
	if err := ctx.Err(); err != nil {
		return protocol.CoreInstallResult{}, err
	}
	result := f.installResult
	result.Schema = "mihari/v1"
	if result.Revision == 0 {
		result.Revision = f.status.Revision + 1
	}
	return result, nil
}
func (f *fakeClient) AddSubscription(_ context.Context, request protocol.SubscriptionAddRequest) (protocol.SubscriptionResult, error) {
	f.addCalls++
	result := f.subscriptionResult
	result.Schema = "mihari/v1"
	if result.Revision == 0 {
		result.Revision = f.status.Revision + 1
	}
	if result.Subscription.Name == "" {
		result.Subscription.Name = request.Name
	}
	return result, nil
}
func (f *fakeClient) UpdateGeoIP(_ context.Context, request protocol.MutationRequest) (protocol.GeoIPUpdateResult, error) {
	f.geoIPCalls++
	f.geoIPRequest = request
	result := f.geoIPUpdateResult
	result.Schema = "mihari/v1"
	if result.Revision == 0 {
		result.Revision = f.status.Revision + 1
	}
	return result, nil
}
func (f *fakeClient) UpdateOnboarding(_ context.Context, request protocol.OnboardingUpdateRequest) (protocol.OnboardingStatus, error) {
	f.updateCalls++
	f.update = request
	f.status.Complete = request.Complete != nil && *request.Complete
	f.status.Revision++
	return f.status, nil
}
func (f *fakeClient) Core(context.Context) (protocol.CoreStatus, error) {
	f.coreCalls++
	return f.coreStatus, f.coreErr
}
func (f *fakeClient) GeoIPStatus(context.Context) (protocol.GeoIPStatus, error) {
	f.geoIPStatusCalls++
	return f.geoIPStatus, f.geoIPErr
}
func (f *fakeClient) ServiceStatus(context.Context) (protocol.ServiceStatus, error) {
	f.serviceCalls++
	return f.serviceStatus, f.serviceErr
}

func TestSetupFormUsesTabOnlyToMoveBetweenEndpointFields(t *testing.T) {
	model := loadedModel(&fakeClient{status: defaultStatus(false)})
	if model.focusedField != 0 {
		t.Fatalf("field=%d", model.focusedField)
	}
	model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyTab})
	if model.focusedField != 1 {
		t.Fatalf("tab field=%d", model.focusedField)
	}
	model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
	if model.focusedField != 1 {
		t.Fatalf("down moved form focus=%d", model.focusedField)
	}
	before := model.inputs[1].Value()
	model = updateKey(t, model, tea.KeyPressMsg{Code: 'q', Text: "q"})
	if got := model.inputs[1].Value(); got != before+"q" {
		t.Fatalf("text input=%q want=%q", got, before+"q")
	}
}

func TestSetup_PasteMsgInsertsIntoFocusedField(t *testing.T) {
	model := loadedModel(&fakeClient{status: defaultStatus(false)})
	model.inputs[0].SetValue("")
	model.focusEndpoint(0)
	updated, _ := model.Update(tea.PasteMsg{Content: "127.0.0.1:8080"})
	model = updated.(*Model)
	if got := model.inputs[0].Value(); got != "127.0.0.1:8080" {
		t.Fatalf("endpoint value=%q", got)
	}
}

func TestSetup_IgnoresPasteWhileLoading(t *testing.T) {
	model := loadedModel(&fakeClient{status: defaultStatus(false)})
	model.inputs[0].SetValue("127.0.0.1:9090")
	model.focusEndpoint(0)
	model.loading = true
	updated, _ := model.Update(tea.PasteMsg{Content: "127.0.0.1:17890"})
	model = updated.(*Model)
	if got := model.inputs[0].Value(); got != "127.0.0.1:9090" {
		t.Fatalf("loading paste should be ignored, got=%q", got)
	}
}

func TestSetup_SubscriptionPasteMsgInsertsURL(t *testing.T) {
	model := loadedModel(&fakeClient{status: defaultStatus(false)})
	model.step = stepSubscription
	model.subscriptionInputs = subscriptionInputs()
	model.focusSubscription(1)
	updated, _ := model.Update(tea.PasteMsg{Content: "https://example.test/sub"})
	model = updated.(*Model)
	if got := model.subscriptionInputs[1].Value(); got != "https://example.test/sub" {
		t.Fatalf("url value=%q", got)
	}
}

func TestSetupEscapeAtFirstStepRequestsCancelWithoutCompleting(t *testing.T) {
	client := &fakeClient{status: defaultStatus(true)}
	model := loadedModel(client)
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = updated.(*Model)
	if command == nil || client.updateCalls != 0 {
		t.Fatalf("command=%v updates=%d", command != nil, client.updateCalls)
	}
	if _, ok := command().(CancelledMsg); !ok || model.status.Complete != true {
		t.Fatalf("message=%T status=%#v", command(), model.status)
	}
}

func TestSetupRejectsInvalidOrCollidingEndpointsBeforeAdvancing(t *testing.T) {
	model := loadedModel(&fakeClient{status: defaultStatus(false)})
	model.inputs[1].SetValue("0.0.0.0:9090")
	model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.step != stepEndpoints || !strings.Contains(model.View(), "loopback") {
		t.Fatalf("step=%v view=%s", model.step, model.View())
	}
	model.inputs[1].SetValue("127.0.0.1:9190")
	model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.step != stepEndpoints || !strings.Contains(model.View(), "distinct") {
		t.Fatalf("step=%v view=%s", model.step, model.View())
	}
}

func TestSetupRunsCoreOptionalSubscriptionGeoIPAndCompletes(t *testing.T) {
	client := &fakeClient{status: defaultStatus(false)}
	model := loadedModel(client)
	model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.step != stepCore {
		t.Fatalf("step=%v", model.step)
	}
	model = runKeyCommand(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.step != stepSubscription || client.installCalls != 1 {
		t.Fatalf("step=%v installs=%d", model.step, client.installCalls)
	}
	model.subscriptionInputs[0].SetValue("Main")
	model.subscriptionInputs[1].SetValue("https://example.test/sub")
	model = runKeyCommand(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.step != stepGeoIP || client.addCalls != 1 {
		t.Fatalf("step=%v adds=%d", model.step, client.addCalls)
	}
	model = runKeyCommand(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.step != stepReview || client.geoIPCalls != 1 {
		t.Fatalf("step=%v geoip=%d", model.step, client.geoIPCalls)
	}
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if command == nil {
		t.Fatal("review did not start completion")
	}
	updated, command = model.Update(command())
	model = updated.(*Model)
	if client.updateCalls != 1 || command == nil {
		t.Fatalf("updates=%d completed command=%v", client.updateCalls, command != nil)
	}
	if _, ok := command().(CompletedMsg); !ok || model.status.Complete != true {
		t.Fatalf("message=%T status=%#v", command(), model.status)
	}
}

func TestSetupSubscriptionFormTreatsSAsTextAndBlankEnterSkips(t *testing.T) {
	model := loadedModel(&fakeClient{status: defaultStatus(false)})
	model.step = stepSubscription
	model.focusSubscription(0)
	if strings.Contains(model.View(), "s skip") {
		t.Fatalf("subscription help advertises an inactive skip key: %s", model.View())
	}
	model = updateKey(t, model, tea.KeyPressMsg{Code: 's', Text: "s"})
	if model.step != stepSubscription || model.subscriptionInputs[0].Value() != "s" {
		t.Fatalf("step=%v name=%q", model.step, model.subscriptionInputs[0].Value())
	}
	model.subscriptionInputs[0].SetValue("")
	model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.step != stepGeoIP {
		t.Fatalf("blank optional subscription did not skip: step=%v", model.step)
	}
}

func TestSetupRevisionConflictReloadsAuthoritativeState(t *testing.T) {
	client := &fakeClient{status: defaultStatus(false)}
	model := loadedModel(client)
	updated, command := model.Update(actionResultMsg{err: protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "changed"}})
	model = updated.(*Model)
	if command == nil || !model.loading {
		t.Fatalf("reload command=%v loading=%v", command != nil, model.loading)
	}
	client.status.Revision = 9
	updated, _ = model.Update(command())
	model = updated.(*Model)
	if model.status.Revision != 9 || client.onboardingCalls != 1 || !strings.Contains(model.View(), ui.SetupChangedMessage) {
		t.Fatalf("status=%#v calls=%d view=%s", model.status, client.onboardingCalls, model.View())
	}
}

func TestSetupCompletionConflictReloadsAuthoritativeState(t *testing.T) {
	client := &fakeClient{status: defaultStatus(false)}
	model := loadedModel(client)
	updated, command := model.Update(completeResultMsg{err: protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "changed"}})
	model = updated.(*Model)
	if command == nil || !model.loading {
		t.Fatalf("reload command=%v loading=%v", command != nil, model.loading)
	}
}

func TestSetupCommandsUseOwnedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeClient{status: defaultStatus(false)}
	model := NewWithContext(ctx, client, func() string { return "setup-op" })
	updated, _ := model.Update(onboardingResultMsg{status: client.status})
	model = updated.(*Model)
	model.step = stepCore
	cancel()
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = updated.(*Model)
	if command == nil {
		t.Fatal("install command missing")
	}
	message := command().(actionResultMsg)
	if !errors.Is(message.err, context.Canceled) {
		t.Fatalf("err=%v", message.err)
	}
}

func TestSetupConfirmsBeforeChangingCompletedEffectiveConfiguration(t *testing.T) {
	client := &fakeClient{status: defaultStatus(true)}
	model := loadedModel(client)
	model.inputs[2].SetValue("127.0.0.1:9292")
	model.step = stepReview
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if command == nil {
		t.Fatal("missing confirmation request")
	}
	request, ok := command().(ui.ActionIntentMsg)
	if !ok || request.Execute == nil || request.Action != ui.ActionApplyEndpointChange || client.updateCalls != 0 {
		t.Fatalf("message=%T updates=%d", command(), client.updateCalls)
	}
	updated, _ = model.Update(request.Execute())
	_ = updated.(*Model)
	if client.updateCalls != 1 {
		t.Fatalf("updates=%d", client.updateCalls)
	}
}

func TestSetupReviewShowsEndpointsWithoutLegacyWebGUIUnavailableCopy(t *testing.T) {
	model := loadedModel(&fakeClient{status: defaultStatus(false)})
	model.step = stepReview
	view := model.View()
	for _, want := range []string{
		ui.SetupReviewTitle,
		"Mixed        127.0.0.1:9190",
		"Controller   127.0.0.1:9090",
		"Web          127.0.0.1:9191",
		ui.SetupCompleteHelp,
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in review:\n%s", want, view)
		}
	}
	for _, banned := range []string{
		"Unavailable in this build",
		"does not block setup",
		"Phase 5",
	} {
		if strings.Contains(view, banned) {
			t.Fatalf("review still has legacy Web GUI residual %q:\n%s", banned, view)
		}
	}
}

func defaultStatus(complete bool) protocol.OnboardingStatus {
	return protocol.OnboardingStatus{Schema: "mihari/v1", Revision: 4, Complete: complete, MixedAddr: "127.0.0.1:9190", ControllerAddr: "127.0.0.1:9090", WebAddr: "127.0.0.1:9191"}
}

func loadedModel(client *fakeClient) *Model {
	model := New(client, func() string { return "setup-op" })
	updated, _ := model.Update(onboardingResultMsg{status: client.status})
	return updated.(*Model)
}

func updateKey(t *testing.T, model *Model, key tea.KeyPressMsg) *Model {
	t.Helper()
	updated, _ := model.Update(key)
	return updated.(*Model)
}

func runKeyCommand(t *testing.T, model *Model, key tea.KeyPressMsg) *Model {
	t.Helper()
	updated, command := model.Update(key)
	model = updated.(*Model)
	if command == nil {
		t.Fatal("expected command")
	}
	updated, _ = model.Update(command())
	return updated.(*Model)
}

func TestSetupCoreAndGeoIPRequestsCarrySetupSource(t *testing.T) {
	client := &fakeClient{status: defaultStatus(false)}
	model := loadedModel(client)

	_ = model.installCore()()
	if client.installRequest.Source != "setup" {
		t.Fatalf("install source=%q want setup", client.installRequest.Source)
	}

	_ = model.updateGeoIP()()
	if client.geoIPRequest.Source != "setup" {
		t.Fatalf("geoip source=%q want setup", client.geoIPRequest.Source)
	}
}

func TestSetupStepCoreShowsLocalCoreReady(t *testing.T) {
	client := &fakeClient{status: defaultStatus(false), coreStatus: protocol.CoreStatus{LocalReady: true, LocalVersion: "v1.18.5"}}
	model := loadedModel(client)
	model.step = stepCore
	updated, _ := model.Update(model.fetchCoreLocal()())
	model = updated.(*Model)
	view := model.View()
	if !strings.Contains(view, "v1.18.5") || strings.Contains(view, ui.SetupCoreWillDownload) {
		t.Fatalf("ready view missing version or shows will-download:\n%s", view)
	}
	if client.coreCalls != 1 {
		t.Fatalf("coreCalls=%d", client.coreCalls)
	}
}

func TestSetupStepCoreShowsWillDownloadWhenNotReady(t *testing.T) {
	client := &fakeClient{status: defaultStatus(false), coreStatus: protocol.CoreStatus{LocalReady: false}}
	model := loadedModel(client)
	model.step = stepCore
	updated, _ := model.Update(model.fetchCoreLocal()())
	model = updated.(*Model)
	if !strings.Contains(model.View(), ui.SetupCoreWillDownload) {
		t.Fatalf("view=%s", model.View())
	}
}

func TestSetupStepGeoIPShowsLocalReadyAndSkipStillWorks(t *testing.T) {
	client := &fakeClient{status: defaultStatus(false), geoIPStatus: protocol.GeoIPStatus{Country: protocol.GeoIPDatabaseStatus{Available: true}, ASN: protocol.GeoIPDatabaseStatus{Available: true}}}
	model := loadedModel(client)
	model.step = stepGeoIP
	updated, _ := model.Update(model.fetchGeoIPLocal()())
	model = updated.(*Model)
	if !strings.Contains(model.View(), ui.SetupGeoIPLocalReady) {
		t.Fatalf("view=%s", model.View())
	}
	model = updateKey(t, model, tea.KeyPressMsg{Code: 's', Text: "s"})
	if model.step != stepReview {
		t.Fatalf("skip did not advance: step=%v", model.step)
	}
}

func TestSetupLocalDetectionStaleResultsIgnored(t *testing.T) {
	client := &fakeClient{status: defaultStatus(false), coreStatus: protocol.CoreStatus{LocalReady: true, LocalVersion: "v1.18.5"}}
	model := loadedModel(client)
	model.step = stepCore
	first := model.fetchCoreLocal()
	model.fetchCoreLocal() // bump generation, making first stale
	updated, _ := model.Update(first())
	model = updated.(*Model)
	if model.coreLocalLoaded {
		t.Fatalf("stale core result was applied: loaded=%v", model.coreLocalLoaded)
	}
}

func TestSetupLocalDetectionFallsBackToStaticOnProbeFailure(t *testing.T) {
	client := &fakeClient{status: defaultStatus(false), coreErr: protocol.APIError{Code: protocol.CodeDaemonUnavailable}}
	model := loadedModel(client)
	model.step = stepCore
	updated, _ := model.Update(model.fetchCoreLocal()())
	model = updated.(*Model)
	if model.coreLocalLoaded {
		t.Fatalf("probe error should not set loaded")
	}
	view := model.View()
	if !strings.Contains(view, ui.SetupCoreBody) {
		t.Fatalf("static fallback missing:\n%s", view)
	}
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if !model.loading || command == nil {
		t.Fatalf("enter blocked after probe failure: loading=%v command=%v", model.loading, command != nil)
	}
}

func resolveServiceStatus(t *testing.T, model *Model) *Model {
	t.Helper()
	updated, _ := model.Update(model.fetchServiceStatus()())
	return updated.(*Model)
}

func TestSetupReviewSummarizesLocalCoreAndSubscriptionAndGeoIP(t *testing.T) {
	client := &fakeClient{
		status:             defaultStatus(false),
		installResult:      protocol.CoreInstallResult{Version: "v1.18.5", Updated: false},
		subscriptionResult: protocol.SubscriptionResult{Subscription: protocol.Subscription{Name: "Main"}},
		geoIPUpdateResult:  protocol.GeoIPUpdateResult{Status: protocol.GeoIPStatus{Country: protocol.GeoIPDatabaseStatus{Available: true}, ASN: protocol.GeoIPDatabaseStatus{Available: true}}},
		serviceStatus:      protocol.ServiceStatus{Schema: "mihari/v1", Status: "running"},
	}
	model := loadedModel(client)
	model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	model = runKeyCommand(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	model.subscriptionInputs[0].SetValue("Main")
	model.subscriptionInputs[1].SetValue("https://example.test/sub")
	model = runKeyCommand(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	model = runKeyCommand(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.step != stepReview {
		t.Fatalf("step=%v", model.step)
	}
	model = resolveServiceStatus(t, model)
	view := model.View()
	for _, want := range []string{"v1.18.5", ui.SetupReviewCoreLocal, "Main", ui.SetupReviewGeoIPReady, "running"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in review:\n%s", want, view)
		}
	}
}

func TestSetupReviewMarksFreshCoreInstall(t *testing.T) {
	client := &fakeClient{status: defaultStatus(false), installResult: protocol.CoreInstallResult{Version: "v1.18.7", Updated: true}}
	model := loadedModel(client)
	model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	model = runKeyCommand(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	model = runKeyCommand(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	model = runKeyCommand(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.step != stepReview {
		t.Fatalf("step=%v", model.step)
	}
	if !strings.Contains(model.View(), ui.SetupReviewCoreFresh) {
		t.Fatalf("fresh core missing:\n%s", model.View())
	}
}

func TestSetupReviewShowsSkippedSubscriptionAndGeoIP(t *testing.T) {
	client := &fakeClient{status: defaultStatus(false)}
	model := loadedModel(client)
	model.step = stepGeoIP
	model = updateKey(t, model, tea.KeyPressMsg{Code: 's', Text: "s"})
	if model.step != stepReview || !model.geoipSkipped {
		t.Fatalf("step=%v skipped=%v", model.step, model.geoipSkipped)
	}
	model = resolveServiceStatus(t, model)
	view := model.View()
	if !strings.Contains(view, ui.SetupReviewSubscriptionNone) {
		t.Fatalf("subscription none missing:\n%s", view)
	}
	if !strings.Contains(view, ui.SetupReviewGeoIPSkipped) {
		t.Fatalf("geoip skipped missing:\n%s", view)
	}
}

func TestSetupReviewShowsRestartRequiredWhenEndpointsChanged(t *testing.T) {
	client := &fakeClient{status: defaultStatus(false)}
	model := loadedModel(client)
	model.inputs[0].SetValue("127.0.0.1:9290")
	model.status.RestartRequired = true
	model.step = stepReview
	if !strings.Contains(model.View(), ui.SetupReviewRestartRequired) {
		t.Fatalf("restart hint missing:\n%s", model.View())
	}
}

func TestSetupReviewShowsServiceStatus(t *testing.T) {
	running := &fakeClient{status: defaultStatus(false), serviceStatus: protocol.ServiceStatus{Status: "running"}}
	model := loadedModel(running)
	model.step = stepReview
	model = resolveServiceStatus(t, model)
	if !strings.Contains(model.View(), "running") {
		t.Fatalf("running view:\n%s", model.View())
	}

	notInstalled := &fakeClient{status: defaultStatus(false), serviceStatus: protocol.ServiceStatus{Status: "not_installed"}}
	model = loadedModel(notInstalled)
	model.step = stepReview
	model = resolveServiceStatus(t, model)
	if !strings.Contains(model.View(), ui.SetupReviewServiceNotRegistered) {
		t.Fatalf("not_installed view:\n%s", model.View())
	}

	stale := &fakeClient{status: defaultStatus(false), serviceStatus: protocol.ServiceStatus{Status: "running"}}
	model = loadedModel(stale)
	model.step = stepReview
	first := model.fetchServiceStatus()
	model.fetchServiceStatus()
	updated, _ := model.Update(first())
	model = updated.(*Model)
	if model.serviceLoaded {
		t.Fatalf("stale service result applied")
	}
}

func TestProbeEndpointDetectsOccupiedAndFree(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	occupied := listener.Addr().String()
	if got := probeEndpoint(occupied); got != portOccupied {
		t.Fatalf("probeEndpoint(occupied)=%v want portOccupied", got)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := probeEndpoint(occupied); got != portFree {
		t.Fatalf("probeEndpoint(freed)=%v want portFree", got)
	}
}

func TestFindAvailablePortsSkipsOccupied(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	occupied := listener.Addr().String()
	current := [3]string{occupied, "127.0.0.1:20001", "127.0.0.1:20002"}
	result := findAvailablePorts(current)
	if result[0] == occupied {
		t.Fatalf("occupied port not replaced: %s", result[0])
	}
	if got := probeEndpoint(result[0]); got != portFree {
		t.Fatalf("replacement %s probe=%v want portFree", result[0], got)
	}
	if result[0] == result[1] || result[0] == result[2] || result[1] == result[2] {
		t.Fatalf("results not distinct: %v", result)
	}
}

func TestSetupEndpointsMarksOccupiedPortRed(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	occupied := listener.Addr().String()
	model := loadedModel(&fakeClient{status: defaultStatus(false)})
	model.inputs[0].SetValue(occupied)
	updated, _ := model.Update(model.probePorts()())
	model = updated.(*Model)
	if model.portProbe[0] != portOccupied {
		t.Fatalf("portProbe[0]=%v want portOccupied", model.portProbe[0])
	}
	if !strings.Contains(model.View(), ui.SetupPortInUse) {
		t.Fatalf("port in-use marker missing:\n%s", model.View())
	}
}

func TestSetupEndpointsEnterAutoFixesOccupiedPorts(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	occupied := listener.Addr().String()
	model := loadedModel(&fakeClient{status: defaultStatus(false)})
	model.inputs[0].SetValue(occupied)
	model.inputs[1].SetValue("127.0.0.1:20001")
	model.inputs[2].SetValue("127.0.0.1:20002")
	updated, _ := model.Update(model.probePorts()())
	model = updated.(*Model)
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if model.step != stepEndpoints {
		t.Fatalf("advanced before auto-fix: step=%v", model.step)
	}
	if command == nil {
		t.Fatal("expected re-probe command after auto-fix")
	}
	updated, _ = model.Update(command())
	model = updated.(*Model)
	if model.inputs[0].Value() == occupied {
		t.Fatalf("occupied port not rewritten: %s", model.inputs[0].Value())
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if model.step != stepCore {
		t.Fatalf("did not advance after fix: step=%v", model.step)
	}
}

func TestSetupEndpointsEnterAdvancesWhenAllFree(t *testing.T) {
	model := loadedModel(&fakeClient{status: defaultStatus(false)})
	model.portProbe = [3]portState{portFree, portFree, portFree}
	model.portProbeLoaded = true
	model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.step != stepCore {
		t.Fatalf("step=%v want stepCore", model.step)
	}
}

func TestSetupPortProbeStaleResultsIgnored(t *testing.T) {
	model := loadedModel(&fakeClient{status: defaultStatus(false)})
	first := model.probePorts()
	model.probePorts()
	updated, _ := model.Update(first())
	model = updated.(*Model)
	if model.portProbeLoaded {
		t.Fatalf("stale probe result applied")
	}
}

func TestSetupPortProbeDoesNotBlockOnUnknown(t *testing.T) {
	model := loadedModel(&fakeClient{status: defaultStatus(false)})
	model.probe = func(string) portState { return portUnknown }
	updated, _ := model.Update(model.probePorts()())
	model = updated.(*Model)
	view := model.View()
	if strings.Contains(view, ui.SetupPortInUse) {
		t.Fatalf("unknown port marked in-use:\n%s", view)
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if model.step != stepCore {
		t.Fatalf("unknown port blocked enter: step=%v", model.step)
	}
}
