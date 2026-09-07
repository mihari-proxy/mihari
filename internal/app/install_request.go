package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

const (
	InstallRequestSchema = "mihari.install-request/v1"
	InstallResultSchema  = "mihari.install-result/v1"

	MaxInstallRequestBytes = 64 << 10

	InstallOperationInstall   = "install"
	InstallOperationReinstall = "reinstall"
	InstallOperationUpdate    = "update"
	InstallOperationRecover   = "recover"

	InstallChannelMain = "main"
	InstallChannelDev  = "dev"

	InstallLayoutSystem  = "system"
	InstallLayoutPrivate = "private"

	InstallServiceRunning      = "running"
	InstallServiceStopped      = "stopped"
	InstallServiceNotInstalled = "not_installed"
)

// InstallRequest is the R4 §9 apply JSON contract.
type InstallRequest struct {
	Schema         string `json:"schema"`
	Operation      string `json:"operation"`
	Binary         string `json:"binary,omitempty"`
	Bundle         string `json:"bundle,omitempty"`
	Source         string `json:"source,omitempty"`
	Channel        string `json:"channel,omitempty"`
	Layout         string `json:"layout,omitempty"`
	Data           string `json:"data,omitempty"`
	Endpoint       string `json:"endpoint,omitempty"`
	Credential     string `json:"credential,omitempty"`
	InstallRoot    string `json:"install_root,omitempty"`
	PathBinary     string `json:"path_binary,omitempty"`
	ReleaseTag     string `json:"release_tag,omitempty"`
	ArtifactSHA256 string `json:"artifact_sha256,omitempty"`
	BundleSHA256   string `json:"bundle_sha256,omitempty"`
}

// InstallResult is the R4 §9 success JSON contract.
type InstallResult struct {
	Schema         string `json:"schema"`
	Changed        bool   `json:"changed"`
	ServiceStatus  string `json:"service_status"`
	TransactionID  string `json:"transaction_id"`
	SourceRetained bool   `json:"source_retained"`
}

// DecodeInstallRequest strictly decodes a bounded install request.
func DecodeInstallRequest(reader io.Reader) (InstallRequest, error) {
	var request InstallRequest
	keys, err := decodeStrictJSON(reader, MaxInstallRequestBytes, &request)
	if err != nil {
		return InstallRequest{}, invalidInstallRequest()
	}
	if err := validateInstallRequest(request, keys); err != nil {
		return InstallRequest{}, err
	}
	return request, nil
}

// EncodeInstallRequest validates and marshals a request.
func EncodeInstallRequest(request InstallRequest) ([]byte, error) {
	if err := validateInstallRequest(request, installRequestKeys(request)); err != nil {
		return nil, err
	}
	return json.Marshal(request)
}

// DecodeInstallResult strictly decodes a success result document.
func DecodeInstallResult(reader io.Reader) (InstallResult, error) {
	var result InstallResult
	if _, err := decodeStrictJSON(reader, MaxInstallRequestBytes, &result); err != nil {
		return InstallResult{}, invalidInstallResult()
	}
	if err := validateInstallResult(result); err != nil {
		return InstallResult{}, err
	}
	return result, nil
}

// EncodeInstallResult validates and marshals a success result.
func EncodeInstallResult(result InstallResult) ([]byte, error) {
	if err := validateInstallResult(result); err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func validateInstallRequest(request InstallRequest, keys map[string]bool) error {
	if request.Schema != InstallRequestSchema {
		return invalidInstallRequest()
	}
	switch request.Operation {
	case InstallOperationRecover:
		for key := range keys {
			if key != "schema" && key != "operation" {
				return invalidInstallRequest()
			}
		}
		return nil
	case InstallOperationInstall, InstallOperationReinstall, InstallOperationUpdate:
	default:
		return invalidInstallRequest()
	}
	if !keys["binary"] || !keys["channel"] || !keys["layout"] || !validAbsPath(request.Binary) {
		return invalidInstallRequest()
	}
	if request.Channel != InstallChannelMain && request.Channel != InstallChannelDev {
		return invalidInstallRequest()
	}
	switch request.Layout {
	case InstallLayoutSystem:
		if keys["data"] {
			return invalidInstallRequest()
		}
	case InstallLayoutPrivate:
		if !keys["data"] || !validAbsPath(request.Data) {
			return invalidInstallRequest()
		}
	default:
		return invalidInstallRequest()
	}
	for _, field := range []struct{ key, value string }{
		{"bundle", request.Bundle},
		{"source", request.Source},
		{"endpoint", request.Endpoint},
		{"credential", request.Credential},
		{"install_root", request.InstallRoot},
		{"path_binary", request.PathBinary},
	} {
		if keys[field.key] && !validAbsPath(field.value) {
			return invalidInstallRequest()
		}
	}
	if keys["release_tag"] && request.ReleaseTag == "" {
		return invalidInstallRequest()
	}
	if keys["artifact_sha256"] && !validSHA256(request.ArtifactSHA256) {
		return invalidInstallRequest()
	}
	if keys["bundle"] {
		if !keys["bundle_sha256"] || !validSHA256(request.BundleSHA256) {
			return invalidInstallRequest()
		}
	} else if keys["bundle_sha256"] {
		return invalidInstallRequest()
	}
	return nil
}

func installRequestKeys(request InstallRequest) map[string]bool {
	keys := map[string]bool{"schema": true, "operation": true}
	add := func(key, value string) {
		if value != "" {
			keys[key] = true
		}
	}
	add("binary", request.Binary)
	add("bundle", request.Bundle)
	add("source", request.Source)
	add("channel", request.Channel)
	add("layout", request.Layout)
	add("data", request.Data)
	add("endpoint", request.Endpoint)
	add("credential", request.Credential)
	add("install_root", request.InstallRoot)
	add("path_binary", request.PathBinary)
	add("release_tag", request.ReleaseTag)
	add("artifact_sha256", request.ArtifactSHA256)
	add("bundle_sha256", request.BundleSHA256)
	return keys
}

func validateInstallResult(result InstallResult) error {
	if result.Schema != InstallResultSchema || !validTransactionID(result.TransactionID) {
		return invalidInstallResult()
	}
	switch result.ServiceStatus {
	case InstallServiceRunning, InstallServiceStopped, InstallServiceNotInstalled:
		return nil
	default:
		return invalidInstallResult()
	}
}

func decodeStrictJSON(reader io.Reader, max int, dest any) (map[string]bool, error) {
	if reader == nil || max <= 0 {
		return nil, os.ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(reader, int64(max)+1))
	if err != nil || len(data) > max || !utf8.Valid(data) {
		return nil, os.ErrInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	keys, err := uniqueJSONObject(dec)
	if err != nil {
		return nil, os.ErrInvalid
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, os.ErrInvalid
	}
	dec = json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return nil, os.ErrInvalid
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, os.ErrInvalid
	}
	return keys, nil
}

func uniqueJSONObject(dec *json.Decoder) (map[string]bool, error) {
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return nil, os.ErrInvalid
	}
	keys := map[string]bool{}
	for dec.More() {
		key, err := dec.Token()
		name, ok := key.(string)
		if err != nil || !ok || keys[name] {
			return nil, os.ErrInvalid
		}
		keys[name] = true
		if err := uniqueJSONValue(dec, 1); err != nil {
			return nil, err
		}
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') {
		return nil, os.ErrInvalid
	}
	return keys, nil
}

func uniqueJSONValue(dec *json.Decoder, depth int) error {
	if depth > 16 {
		return os.ErrInvalid
	}
	token, err := dec.Token()
	if err != nil || token == nil {
		return os.ErrInvalid
	}
	switch token {
	case json.Delim('{'):
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] {
				return os.ErrInvalid
			}
			seen[name] = true
			if err := uniqueJSONValue(dec, depth+1); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	case json.Delim('['):
		for dec.More() {
			if err := uniqueJSONValue(dec, depth+1); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	}
	return nil
}

func validAbsPath(path string) bool {
	if path == "" || strings.ContainsRune(path, 0) {
		return false
	}
	if path[0] == '/' {
		return true
	}
	return filepath.IsAbs(path)
}

func validSHA256(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == value
}

func validTransactionID(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 16 && hex.EncodeToString(raw) == value
}

func invalidInstallRequest() error {
	return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid install request"}
}

func invalidInstallResult() error {
	return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid install result"}
}

// ReadInstallRequestFile reads a bounded, regular, no-follow request on Unix.
// The returned values are untrusted inputs; authorization is the caller's euid.
func ReadInstallRequestFile(ctx context.Context, path string) (InstallRequest, error) {
	if err := ctx.Err(); err != nil {
		return InstallRequest{}, err
	}
	if !filepath.IsAbs(path) {
		return InstallRequest{}, invalidInstallRequest()
	}
	raw, err := readHostFile(path, MaxInstallRequestBytes)
	if err != nil {
		return InstallRequest{}, invalidInstallRequest()
	}
	return DecodeInstallRequest(bytes.NewReader(raw))
}
