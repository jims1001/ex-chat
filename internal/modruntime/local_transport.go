package modruntime

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

// localContractTransport preserves contract validation/receipts without opening
// an internal listener or granting an externally routable authentication token.
type localContractTransport struct{ handler http.Handler }
type localContractResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *localContractResponse) Header() http.Header { return w.header }
func (w *localContractResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *localContractResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}
func (t localContractTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	response := &localContractResponse{header: make(http.Header)}
	// Never propagate another owner's transaction to the recipient.
	request = request.Clone(contextWithoutTransaction(request.Context()))
	t.handler.ServeHTTP(response, request)
	if response.status == 0 {
		response.status = http.StatusOK
	}
	return &http.Response{StatusCode: response.status, Header: response.header, Body: io.NopCloser(bytes.NewReader(response.body.Bytes())), Request: request}, nil
}

func contextWithoutTransaction(ctx context.Context) context.Context {
	return context.WithValue(ctx, transactionKey{}, struct{}{})
}
