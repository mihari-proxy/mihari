package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.yaml.in/yaml/v3"
)

func init() {
	if os.Getenv("MIHARI_FAKE_MIHOMO") == "1" {
		os.Exit(runFakeMihomo(os.Args[1:]))
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func runFakeMihomo(arguments []string) int {
	for _, argument := range arguments {
		if argument == "-v" {
			fmt.Fprintln(os.Stdout, "Mihomo Meta v1.19.0")
			return 0
		}
	}
	configPath := argumentValue(arguments, "-f")
	if configPath == "" {
		fmt.Fprintf(os.Stderr, "fake mihomo missing -f; args=%q\n", arguments)
		return 2
	}
	if containsArgument(arguments, "-t") {
		if _, err := os.Stat(configPath); err != nil {
			return 1
		}
		return 0
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return 1
	}
	var config struct {
		Controller string `yaml:"external-controller"`
		Secret     string `yaml:"secret"`
	}
	if err := yaml.Unmarshal(raw, &config); err != nil || config.Controller == "" || config.Secret == "" {
		return 1
	}
	mux := http.NewServeMux()
	selected := "DIRECT"
	mux.HandleFunc("GET /version", func(response http.ResponseWriter, _ *http.Request) {
		writeFakeJSON(response, map[string]any{"meta": true, "version": "v1.19.0"})
	})
	mux.HandleFunc("GET /proxies", func(response http.ResponseWriter, _ *http.Request) {
		writeFakeJSON(response, map[string]any{"proxies": map[string]any{
			"GLOBAL": map[string]any{"name": "GLOBAL", "type": "Selector", "now": selected, "all": []string{"DIRECT", "REJECT"}},
		}})
	})
	mux.HandleFunc("PUT /proxies/{name}", func(response http.ResponseWriter, request *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil || body.Name == "" {
			http.Error(response, "bad request", http.StatusBadRequest)
			return
		}
		selected = body.Name
		response.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /group/{name}/delay", func(response http.ResponseWriter, _ *http.Request) {
		writeFakeJSON(response, map[string]uint16{"DIRECT": 1, "REJECT": 2})
	})
	mux.HandleFunc("GET /connections", func(response http.ResponseWriter, _ *http.Request) {
		writeFakeJSON(response, map[string]any{"downloadTotal": 2, "uploadTotal": 3, "connections": []any{map[string]any{"id": "connection-1"}}})
	})
	mux.HandleFunc("DELETE /connections/{id}", func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("DELETE /connections", func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /rules", func(response http.ResponseWriter, _ *http.Request) {
		writeFakeJSON(response, map[string]any{"rules": []any{map[string]any{"type": "MATCH", "payload": "", "proxy": "DIRECT"}}})
	})
	mux.HandleFunc("PUT /configs", func(response http.ResponseWriter, request *http.Request) {
		var body struct {
			Path string `json:"path"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil || body.Path == "" {
			http.Error(response, "bad request", http.StatusBadRequest)
			return
		}
		if _, err := os.Stat(body.Path); err != nil {
			http.Error(response, "missing config", http.StatusUnprocessableEntity)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	})
	for _, stream := range []string{"traffic", "memory", "logs"} {
		stream := stream
		mux.HandleFunc("GET /"+stream, func(response http.ResponseWriter, request *http.Request) {
			connection, err := websocket.Accept(response, request, nil)
			if err != nil {
				return
			}
			defer connection.CloseNow()
			message, _ := json.Marshal(map[string]any{"stream": stream, "value": 1})
			_ = connection.Write(request.Context(), websocket.MessageText, message)
			_ = connection.Close(websocket.StatusNormalClosure, "fixture complete")
		})
	}
	authorized := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+config.Secret {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(response, request)
	})
	server := &http.Server{Addr: config.Controller, Handler: authorized, ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return 1
	}
	return 0
}

func argumentValue(arguments []string, name string) string {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == name {
			return arguments[index+1]
		}
	}
	return ""
}

func containsArgument(arguments []string, name string) bool {
	for _, argument := range arguments {
		if argument == name {
			return true
		}
	}
	return false
}

func writeFakeJSON(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(value)
}

func reserveLoopbackAddresses(t *testing.T, count int) []string {
	t.Helper()
	listeners := make([]net.Listener, 0, count)
	addresses := make([]string, 0, count)
	for range count {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
		addresses = append(addresses, listener.Addr().String())
	}
	for _, listener := range listeners {
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return addresses
}
