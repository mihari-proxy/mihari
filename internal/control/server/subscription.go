package server

import (
	"context"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

type subscriptionAPI interface {
	Subscriptions() subscription.PublicCatalog
	AddSubscription(context.Context, runtimeapi.Operation, runtimeapi.AddSubscriptionInput) (subscription.PublicProfile, error)
	RefreshSubscription(context.Context, runtimeapi.Operation, string) (subscription.PublicProfile, error)
	UseSubscription(context.Context, runtimeapi.Operation, string) (subscription.PublicProfile, error)
	SetSubscriptionEnabled(context.Context, runtimeapi.Operation, string, bool) (subscription.PublicProfile, error)
	SetSubscription(context.Context, runtimeapi.Operation, string, runtimeapi.SetSubscriptionInput) (subscription.PublicProfile, error)
	RemoveSubscription(context.Context, runtimeapi.Operation, string) error
}

func (s *Server) subscriptionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/subscriptions", s.listSubscriptions)
	mux.HandleFunc("GET /v1/subscriptions/{id}", s.showSubscription)
	mux.HandleFunc("POST /v1/subscriptions", s.addSubscription)
	mux.HandleFunc("POST /v1/subscriptions/{id}/refresh", s.refreshSubscription)
	mux.HandleFunc("PUT /v1/subscriptions/{id}/active", s.useSubscription)
	mux.HandleFunc("PUT /v1/subscriptions/{id}/enabled", s.enableSubscription)
	mux.HandleFunc("PATCH /v1/subscriptions/{id}", s.updateSubscription)
	mux.HandleFunc("DELETE /v1/subscriptions/{id}", s.removeSubscription)
}

func (s *Server) subscriptionsRuntime(ctx context.Context, writer http.ResponseWriter) (subscriptionAPI, bool) {
	runtime, ok := s.runtime.(subscriptionAPI)
	if !ok {
		s.writeControlError(ctx, writer, subscriptionsUnavailable())
	}
	return runtime, ok
}

func (s *Server) listSubscriptions(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.subscriptionsRuntime(request.Context(), writer)
	if !ok {
		return
	}
	writeJSON(writer, http.StatusOK, subscriptionListDTO(runtime.Subscriptions(), s.runtime.Snapshot().Revision))
}

func (s *Server) showSubscription(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.subscriptionsRuntime(request.Context(), writer)
	if !ok {
		return
	}
	profile, found := publicProfile(runtime.Subscriptions(), request.PathValue("id"))
	if !found {
		writeInvalidArgument(writer, "subscription not found")
		return
	}
	writeJSON(writer, http.StatusOK, subscriptionResultDTO(profile, "", s.runtime.Snapshot().Revision))
}

func (s *Server) addSubscription(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.subscriptionsRuntime(request.Context(), writer)
	if !ok {
		return
	}
	var body protocol.SubscriptionAddRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	if body.Name == "" || body.URL == "" {
		writeInvalidArgument(writer, "subscription name and URL are required")
		return
	}
	ctx := logging.WithOperation(request.Context(), logging.OperationMetadata{ID: body.OperationID, Name: "subscription.add"})
	profile, err := runtime.AddSubscription(ctx, runtimeapi.Operation{ID: body.OperationID, Source: "control", IfRevision: body.IfRevision}, runtimeapi.AddSubscriptionInput{Name: body.Name, URL: body.URL, ProxyMode: body.ProxyMode})
	if err != nil {
		s.writeControlError(ctx, writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, subscriptionResultDTO(profile, body.OperationID, s.runtime.Snapshot().Revision))
}

func (s *Server) refreshSubscription(writer http.ResponseWriter, request *http.Request) {
	s.subscriptionProfileMutation(writer, request, "subscription.refresh", func(ctx context.Context, runtime subscriptionAPI, operation runtimeapi.Operation, id string) (subscription.PublicProfile, error) {
		return runtime.RefreshSubscription(ctx, operation, id)
	})
}

func (s *Server) useSubscription(writer http.ResponseWriter, request *http.Request) {
	s.subscriptionProfileMutation(writer, request, "subscription.use", func(ctx context.Context, runtime subscriptionAPI, operation runtimeapi.Operation, id string) (subscription.PublicProfile, error) {
		return runtime.UseSubscription(ctx, operation, id)
	})
}

func (s *Server) subscriptionProfileMutation(writer http.ResponseWriter, request *http.Request, operationName string, mutate func(context.Context, subscriptionAPI, runtimeapi.Operation, string) (subscription.PublicProfile, error)) {
	runtime, ok := s.subscriptionsRuntime(request.Context(), writer)
	if !ok {
		return
	}
	var body protocol.MutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	ctx := logging.WithOperation(request.Context(), logging.OperationMetadata{ID: body.OperationID, Name: operationName})
	profile, err := mutate(ctx, runtime, runtimeapi.Operation{ID: body.OperationID, Source: "control", IfRevision: body.IfRevision}, request.PathValue("id"))
	if err != nil {
		s.writeControlError(ctx, writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, subscriptionResultDTO(profile, body.OperationID, s.runtime.Snapshot().Revision))
}

func (s *Server) enableSubscription(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.subscriptionsRuntime(request.Context(), writer)
	if !ok {
		return
	}
	var body protocol.SubscriptionEnabledRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	ctx := logging.WithOperation(request.Context(), logging.OperationMetadata{ID: body.OperationID, Name: "subscription.enabled"})
	profile, err := runtime.SetSubscriptionEnabled(ctx, runtimeapi.Operation{ID: body.OperationID, Source: "control", IfRevision: body.IfRevision}, request.PathValue("id"), body.Enabled)
	if err != nil {
		s.writeControlError(ctx, writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, subscriptionResultDTO(profile, body.OperationID, s.runtime.Snapshot().Revision))
}

func (s *Server) updateSubscription(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.subscriptionsRuntime(request.Context(), writer)
	if !ok {
		return
	}
	var body protocol.SubscriptionUpdateRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	ctx := logging.WithOperation(request.Context(), logging.OperationMetadata{ID: body.OperationID, Name: "subscription.set"})
	profile, err := runtime.SetSubscription(ctx, runtimeapi.Operation{ID: body.OperationID, Source: "control", IfRevision: body.IfRevision}, request.PathValue("id"), runtimeapi.SetSubscriptionInput{
		Name: body.Name, URL: body.URL, Interval: body.Interval, AutoRefresh: body.AutoRefresh, GlobalPeriod: body.GlobalInterval, ProxyMode: body.ProxyMode,
	})
	if err != nil {
		s.writeControlError(ctx, writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, subscriptionResultDTO(profile, body.OperationID, s.runtime.Snapshot().Revision))
}

func (s *Server) removeSubscription(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.subscriptionsRuntime(request.Context(), writer)
	if !ok {
		return
	}
	var body protocol.MutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	ctx := logging.WithOperation(request.Context(), logging.OperationMetadata{ID: body.OperationID, Name: "subscription.remove"})
	if err := runtime.RemoveSubscription(ctx, runtimeapi.Operation{ID: body.OperationID, Source: "control", IfRevision: body.IfRevision}, request.PathValue("id")); err != nil {
		s.writeControlError(ctx, writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, protocol.MutationResult{Schema: "mihari/v1", OperationID: body.OperationID, Revision: s.runtime.Snapshot().Revision})
}

func subscriptionListDTO(catalog subscription.PublicCatalog, revision uint64) protocol.SubscriptionList {
	result := protocol.SubscriptionList{Schema: "mihari/v1", Revision: revision, ActiveID: catalog.ActiveID, GlobalInterval: catalog.GlobalInterval, Subscriptions: make([]protocol.Subscription, 0, len(catalog.Profiles))}
	for _, profile := range catalog.Profiles {
		result.Subscriptions = append(result.Subscriptions, subscriptionDTO(profile))
	}
	return result
}

func subscriptionResultDTO(profile subscription.PublicProfile, operationID string, revision uint64) protocol.SubscriptionResult {
	return protocol.SubscriptionResult{Schema: "mihari/v1", OperationID: operationID, Revision: revision, Subscription: subscriptionDTO(profile)}
}

func subscriptionDTO(profile subscription.PublicProfile) protocol.Subscription {
	return protocol.Subscription{
		ID: profile.ID, Name: profile.Name, Enabled: profile.Enabled, AutoRefresh: profile.AutoRefresh,
		Interval: profile.Interval, Cached: profile.Cached, Generation: profile.Generation,
		UpdatedAt: profile.UpdatedAt, LastError: profile.LastError,
		Upload: profile.Upload, Download: profile.Download, Total: profile.Total, Expire: profile.Expire,
		ProxyMode: profile.ProxyMode,
	}
}

func publicProfile(catalog subscription.PublicCatalog, id string) (subscription.PublicProfile, bool) {
	for _, profile := range catalog.Profiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return subscription.PublicProfile{}, false
}

func subscriptionsUnavailable() error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: "subscription manager is unavailable"}
}
