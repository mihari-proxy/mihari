package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestLocalYAML_IPCCreationRevealAndMissingSource(t *testing.T) {
	f := newSubscriptionControlFixture(t, &editController{})
	path := filepath.Join(t.TempDir(), "配置 space.yaml")
	if err := os.WriteFile(path, []byte("proxies: []\nproxy-providers:\n  local: {type: file, path: nodes.yaml}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	uri, err := platform.FileURI(path)
	if err != nil {
		t.Fatal(err)
	}
	request := protocol.SubscriptionAddRequest{OperationID: "file-unconfirmed", Name: "local", URL: uri}
	_, err = f.client.AddSubscription(context.Background(), request)
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidArgument || api.Details["confirmation_required"] != "file_references" {
		t.Fatalf("err=%v", err)
	}
	request.OperationID = "file-confirmed"
	request.AllowFileReferences = true
	added, err := f.client.AddSubscription(context.Background(), request)
	if err != nil || added.Subscription.SourceType != "file" || !added.Subscription.Cached {
		t.Fatalf("added=%+v err=%v", added, err)
	}
	revealed, err := f.client.SubscriptionURL(context.Background(), added.Subscription.ID)
	if err != nil || revealed.URL != uri {
		t.Fatalf("revealed=%+v err=%v", revealed, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, err = f.client.RefreshSubscription(context.Background(), added.Subscription.ID, protocol.MutationRequest{OperationID: "file-missing"})
	if !errors.As(err, &api) || api.Diagnostic == nil || !strings.Contains(api.Diagnostic.Detail, "配置 space.yaml") {
		t.Fatalf("err=%v", err)
	}
	current, err := f.client.Subscription(context.Background(), added.Subscription.ID)
	if err != nil || !current.Subscription.Cached || current.Subscription.Generation != added.Subscription.Generation || current.Subscription.LastError == "" {
		t.Fatalf("current=%+v err=%v", current, err)
	}
}
