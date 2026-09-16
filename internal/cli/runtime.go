package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func runtimeClient(dependencies Dependencies) (RuntimeClient, error) {
	if dependencies.RuntimeClient == nil {
		return nil, protocol.APIError{Code: protocol.CodeInternal, Message: "runtime client is unavailable"}
	}
	return dependencies.RuntimeClient, nil
}

func operationID(dependencies Dependencies) (string, error) {
	if dependencies.NewOperationID != nil {
		if id := dependencies.NewOperationID(); id != "" {
			return id, nil
		}
		return "", protocol.APIError{Code: protocol.CodeInternal, Message: "operation ID generation failed"}
	}
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInternal, Message: "operation ID generation failed"}, err)
	}
	return hex.EncodeToString(value[:]), nil
}

func renderJSON(writer io.Writer, value any) error {
	return json.NewEncoder(writer).Encode(value)
}

func classifyRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	var apiError protocol.APIError
	if errors.As(err, &apiError) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDaemonUnavailable, Message: "daemon is unavailable"}, err)
}

func mutationRequest(dependencies Dependencies) (protocol.MutationRequest, error) {
	id, err := operationID(dependencies)
	if err != nil {
		return protocol.MutationRequest{}, err
	}
	return protocol.MutationRequest{OperationID: id}, nil
}

func printMutation(writer io.Writer, result protocol.MutationResult) error {
	_, err := fmt.Fprintf(writer, "Operation %s completed\n", result.OperationID)
	return err
}
