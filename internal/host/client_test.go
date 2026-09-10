package host

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestHTTPResponseAcceptsHostCasingVariants(t *testing.T) {
	for _, raw := range []string{
		`{"StatusCode":200,"Headers":{"X":["y"]},"Body":"T0s="}`,
		`{"status_code":200,"headers":{"X":["y"]},"body":"T0s="}`,
	} {
		var got HTTPResponse
		if err := json.Unmarshal([]byte(raw), &got); err != nil || string(got.Body) != "OK" {
			t.Fatalf("%s: %#v %v", raw, got, err)
		}
		if got.StatusCode != 200 || !reflect.DeepEqual(got.Headers, map[string][]string{"X": {"y"}}) {
			t.Fatalf("decoded response = %#v", got)
		}
	}
}

func TestClientMapsTypedCallsToExactHostOperations(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "marker")
	var operations []string
	caller := func(gotCtx context.Context, operation string, request, response any) error {
		if gotCtx != ctx {
			t.Fatalf("context was not preserved")
		}
		operations = append(operations, operation)
		switch operation {
		case "host.auth.list":
			out, ok := response.(*AuthListResponse)
			if !ok {
				t.Fatalf("list response has type %T", response)
			}
			out.Files = []AuthFile{{AuthIndex: "7", Provider: "codex"}}
		case "host.auth.get":
			out, ok := response.(*AuthGetResponse)
			if !ok {
				t.Fatalf("get response has type %T", response)
			}
			out.JSON = json.RawMessage(`{"access_token":"token"}`)
		case "host.http.do":
			out, ok := response.(*HTTPResponse)
			if !ok {
				t.Fatalf("http response has type %T", response)
			}
			*out = HTTPResponse{StatusCode: 204}
		case "host.log":
			if request == nil {
				t.Fatal("log request must not be nil")
			}
		}
		return nil
	}
	client := NewClient(caller)
	if _, err := client.ListAuthFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetAuth(ctx, "7"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.HTTPDo(ctx, HTTPRequest{Method: "GET", URL: "https://example.test"}); err != nil {
		t.Fatal(err)
	}
	client.Log(ctx, "corr", "message", map[string]any{"account_fingerprint": "abc"})
	want := []string{"host.auth.list", "host.auth.get", "host.http.do", "host.log"}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("operations = %#v, want %#v", operations, want)
	}
}

func TestClientPropagatesCallerErrorWithoutWrappingSecrets(t *testing.T) {
	secretErr := errors.New(`upstream token "fixture-token"`)
	client := NewClient(func(context.Context, string, any, any) error { return secretErr })
	_, err := client.ListAuthFiles(context.Background())
	if !errors.Is(err, secretErr) {
		t.Fatalf("error = %v, want caller error", err)
	}
}
